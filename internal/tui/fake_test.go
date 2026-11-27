package tui

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// fakeDaemon serves a fixed snapshot. Closing ev or fr is the daemon
// going away.
type fakeDaemon struct {
	snap    ipc.SnapshotResult
	ev      chan daemon.Event
	fr      chan daemon.Frame
	pause   func(ids []string) // nil: record only
	add     func(ctx context.Context, p daemon.AddParams) (*store.Job, error)
	details map[string]daemon.Details                // by job id; others get no extras
	refresh func(id, uri string) (*store.Job, error) // nil: the snapshot's job, uri added
	found   []*store.Job                             // Search's results; nil: snapshot jobs whose name holds the query
	config  []ipc.ConfigEntry                        // ConfigList's reply

	detailsCalls atomic.Int32

	mu     sync.Mutex
	paused []string
	calls  []string         // "resume a.iso", "remove a.iso keep", "undo tok-1", ...
	fail   map[string]error // by method name ("ConfigSet"): the error that method returns
	closed bool
}

func (f *fakeDaemon) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

// do records the call line s and returns the error scripted for method.
func (f *fakeDaemon) do(method, s string) error {
	f.record(s)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fail[method]
}

func (f *fakeDaemon) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func newFakeDaemon(jobs ...*store.Job) *fakeDaemon {
	return &fakeDaemon{snap: ipc.SnapshotResult{Jobs: jobs}, ev: make(chan daemon.Event, 8), fr: make(chan daemon.Frame, 1)}
}

func job(num int64, name string, st store.State, actions ...string) *store.Job {
	return &store.Job{ID: name, Num: num, Name: name, State: st, Label: string(st), Actions: actions}
}

func (f *fakeDaemon) List(context.Context, bool, int, ...string) (ipc.SnapshotResult, error) {
	return f.snap, nil
}

func (f *fakeDaemon) Subscribe(context.Context, bool) (<-chan daemon.Event, <-chan daemon.Frame, error) {
	return f.ev, f.fr, nil
}

func (f *fakeDaemon) Status(context.Context) (ipc.Status, error) {
	return ipc.Status{EngineState: "ready", DownloadDir: "/dl"}, nil
}

func (f *fakeDaemon) Add(ctx context.Context, p daemon.AddParams) (*store.Job, error) {
	what := strings.Join(p.URIs, " ")
	if p.Torrent != nil {
		what = fmt.Sprintf("torrent(%d bytes)", len(p.Torrent))
	}
	f.record(fmt.Sprintf("add %s dir=%s out=%s paused=%v force=%v", what, p.Dir, p.Out, p.StartPaused, p.Force))
	if f.add != nil {
		return f.add(ctx, p)
	}
	name := "new"
	if len(p.URIs) > 0 {
		name = path.Base(p.URIs[0])
	}
	return &store.Job{ID: "new", Num: 18, Name: name, State: store.Queued}, nil
}

func (f *fakeDaemon) Pause(_ context.Context, ids ...string) error {
	if f.pause != nil {
		f.pause(ids)
	}
	f.record("pause " + strings.Join(ids, " "))
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, ids...)
	return nil
}

func (f *fakeDaemon) Resume(_ context.Context, ids ...string) error {
	f.record("resume " + strings.Join(ids, " "))
	return nil
}

func (f *fakeDaemon) Remove(ctx context.Context, ids []string, deleteFiles bool) (string, []string, error) {
	if _, ok := ctx.Deadline(); ok {
		f.record("remove with a deadline") // a slow trash would be cut off
	}
	how := "keep"
	if deleteFiles {
		how = "trash"
	}
	f.record("remove " + strings.Join(ids, " ") + " " + how)
	return "tok-1", nil, nil
}

func (f *fakeDaemon) Undo(_ context.Context, token string) error {
	f.record("undo " + token)
	return nil
}
func (f *fakeDaemon) Details(_ context.Context, id string, _ int) (daemon.Details, error) {
	f.detailsCalls.Add(1)
	d := f.details[id]
	for _, j := range f.snap.Jobs {
		if j.ID == id {
			d.Job = j
		}
	}
	return d, nil
}

func (f *fakeDaemon) Refresh(_ context.Context, id, uri string) (*store.Job, error) {
	if err := f.do("Refresh", "refresh "+id+" "+uri); err != nil {
		return nil, err
	}
	if f.refresh != nil {
		return f.refresh(id, uri)
	}
	j := &store.Job{ID: id, State: store.Queued}
	if i := slices.IndexFunc(f.snap.Jobs, func(j *store.Job) bool { return j.ID == id }); i >= 0 {
		c := *f.snap.Jobs[i]
		j = &c
	}
	j.URIs = append(slices.Clone(j.URIs), uri)
	return j, nil
}

func (f *fakeDaemon) SetLimit(_ context.Context, id string, down, up int64) error {
	return f.do("SetLimit", fmt.Sprintf("limit %s %d %d", id, down, up))
}

func (f *fakeDaemon) PauseAll(context.Context) error  { return f.do("PauseAll", "pause all") }
func (f *fakeDaemon) ResumeAll(context.Context) error { return f.do("ResumeAll", "resume all") }
func (f *fakeDaemon) RestartEngine(context.Context) error {
	return f.do("RestartEngine", "restart engine")
}

func (f *fakeDaemon) StopSeeding(_ context.Context, ids ...string) error {
	return f.do("StopSeeding", "stop seeding "+strings.Join(ids, " "))
}

func (f *fakeDaemon) Search(_ context.Context, query string, limit, offset int) ([]*store.Job, error) {
	if err := f.do("Search", fmt.Sprintf("search %s %d %d", query, limit, offset)); err != nil {
		return nil, err
	}
	if f.found != nil {
		return f.found, nil
	}
	var out []*store.Job
	for _, j := range f.snap.Jobs {
		if strings.Contains(j.Name, query) {
			out = append(out, j)
		}
	}
	out = out[min(offset, len(out)):]
	if limit > 0 {
		out = out[:min(limit, len(out))]
	}
	return out, nil
}

func (f *fakeDaemon) ConfigList(context.Context) ([]ipc.ConfigEntry, error) {
	if err := f.do("ConfigList", "config list"); err != nil {
		return nil, err
	}
	return f.config, nil
}

func (f *fakeDaemon) ConfigSet(_ context.Context, key, value string) error {
	return f.do("ConfigSet", "config set "+key+" "+value)
}

func (f *fakeDaemon) ConfigReset(_ context.Context, key string) error {
	return f.do("ConfigReset", "config reset "+key)
}

func (f *fakeDaemon) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// panicDaemon fails the test run loudly if anything calls it.
type panicDaemon struct{}

func (panicDaemon) List(context.Context, bool, int, ...string) (ipc.SnapshotResult, error) {
	panic("List called")
}
func (panicDaemon) Subscribe(context.Context, bool) (<-chan daemon.Event, <-chan daemon.Frame, error) {
	panic("Subscribe called")
}
func (panicDaemon) Status(context.Context) (ipc.Status, error)                { panic("Status called") }
func (panicDaemon) Add(context.Context, daemon.AddParams) (*store.Job, error) { panic("Add called") }
func (panicDaemon) Pause(context.Context, ...string) error                    { panic("Pause called") }
func (panicDaemon) Resume(context.Context, ...string) error                   { panic("Resume called") }
func (panicDaemon) Remove(context.Context, []string, bool) (string, []string, error) {
	panic("Remove called")
}
func (panicDaemon) Undo(context.Context, string) error { panic("Undo called") }
func (panicDaemon) Details(context.Context, string, int) (daemon.Details, error) {
	panic("Details called")
}
func (panicDaemon) Refresh(context.Context, string, string) (*store.Job, error) {
	panic("Refresh called")
}
func (panicDaemon) SetLimit(context.Context, string, int64, int64) error { panic("SetLimit called") }
func (panicDaemon) PauseAll(context.Context) error                       { panic("PauseAll called") }
func (panicDaemon) ResumeAll(context.Context) error                      { panic("ResumeAll called") }
func (panicDaemon) RestartEngine(context.Context) error                  { panic("RestartEngine called") }
func (panicDaemon) StopSeeding(context.Context, ...string) error         { panic("StopSeeding called") }
func (panicDaemon) Search(context.Context, string, int, int) ([]*store.Job, error) {
	panic("Search called")
}
func (panicDaemon) ConfigList(context.Context) ([]ipc.ConfigEntry, error) {
	panic("ConfigList called")
}
func (panicDaemon) ConfigSet(context.Context, string, string) error { panic("ConfigSet called") }
func (panicDaemon) ConfigReset(context.Context, string) error       { panic("ConfigReset called") }
func (panicDaemon) Close() error                                    { panic("Close called") }

// options dials the fakes in turn, then fails.
func options(t *testing.T, ds ...Daemon) Options {
	t.Helper()
	s, _, _, err := config.Load("", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	return Options{
		Settings: s,
		CrashDir: t.TempDir(),
		Prog:     "lazyaria",
		Dial: func(context.Context) (Daemon, error) {
			mu.Lock()
			defer mu.Unlock()
			if len(ds) == 0 {
				return nil, context.DeadlineExceeded
			}
			d := ds[0]
			ds = ds[1:]
			return d, nil
		},
	}
}

// probe wraps the model to keep the last screen it drew. The program's
// output stream only carries changed cells, so tests read the screen here.
type probe struct {
	Model
	last *atomic.Pointer[string]
}

func (p probe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := p.Model.Update(msg)
	return probe{m.(Model), p.last}, cmd
}

func (p probe) View() tea.View {
	v := p.Model.View()
	p.last.Store(&v.Content)
	return v
}

// tui runs the model under teatest at w by h.
type tui struct {
	*teatest.TestModel
	last *atomic.Pointer[string]
}

func startTUI(t *testing.T, o Options, w, h int, opts ...tea.ProgramOption) tui {
	t.Helper()
	p := probe{New(o), &atomic.Pointer[string]{}}
	tm := teatest.NewTestModel(t, p, teatest.WithInitialTermSize(w, h), teatest.WithProgramOptions(opts...))
	return tui{tm, p.last}
}

func (u tui) screen() string {
	if s := u.last.Load(); s != nil {
		return *s
	}
	return ""
}

// waitFor waits until the screen shows want.
func (u tui) waitFor(t *testing.T, want string) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !strings.Contains(u.screen(), want); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %q; the screen is:\n%s", want, u.screen())
		}
	}
}

// quit presses ctrl+c, which quits from any screen, and returns the final model.
func (u tui) quit(t *testing.T) Model {
	t.Helper()
	u.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	return u.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(probe).Model
}

// Each call is recorded with its arguments, and returns the error scripted
// for its method.
func TestFakeRecordsNewCalls(t *testing.T) {
	f := newFakeDaemon(job(1, "a", store.Seeding))
	var d Daemon = f
	ctx := context.Background()
	calls := []struct {
		method, line string
		call         func() error
	}{
		{"Refresh", "refresh a https://e.example/b", func() error { _, err := d.Refresh(ctx, "a", "https://e.example/b"); return err }},
		{"SetLimit", "limit a 1024 0", func() error { return d.SetLimit(ctx, "a", 1024, 0) }},
		{"PauseAll", "pause all", func() error { return d.PauseAll(ctx) }},
		{"ResumeAll", "resume all", func() error { return d.ResumeAll(ctx) }},
		{"RestartEngine", "restart engine", func() error { return d.RestartEngine(ctx) }},
		{"StopSeeding", "stop seeding a b", func() error { return d.StopSeeding(ctx, "a", "b") }},
		{"Search", "search iso 200 400", func() error { _, err := d.Search(ctx, "iso", 200, 400); return err }},
		{"ConfigList", "config list", func() error { _, err := d.ConfigList(ctx); return err }},
		{"ConfigSet", "config set ui.glyphs ascii", func() error { return d.ConfigSet(ctx, "ui.glyphs", "ascii") }},
		{"ConfigReset", "config reset ui.glyphs", func() error { return d.ConfigReset(ctx, "ui.glyphs") }},
	}
	var want []string
	for _, c := range calls {
		if err := c.call(); err != nil {
			t.Errorf("%s: %v with no error scripted", c.method, err)
		}
		want = append(want, c.line)
	}
	if got := f.called(); !slices.Equal(got, want) {
		t.Fatalf("calls %q, want %q", got, want)
	}
	boom := errors.New("boom")
	for _, c := range calls {
		f.fail = map[string]error{c.method: boom}
		if err := c.call(); !errors.Is(err, boom) {
			t.Errorf("%s returned %v, want the scripted error", c.method, err)
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}
