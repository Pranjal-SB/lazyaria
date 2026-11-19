package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// liveDaemon is a manager on a real supervised aria2c.
type liveDaemon struct {
	m      *Manager
	e      engine.Engine
	cancel context.CancelFunc
	done   chan struct{}
}

func startLive(t *testing.T, st *store.Store, root string, tweak func(*config.Settings), setup func(*Manager)) *liveDaemon {
	t.Helper()
	s, _, _, err := config.Load("", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.BT.EnableDHT = false
	if tweak != nil {
		tweak(s)
	}
	p := config.Paths{StateDir: filepath.Join(root, "state"), RuntimeDir: filepath.Join(root, "run"), DownloadDir: filepath.Join(root, "dl")}
	p.AriaConf = filepath.Join(p.RuntimeDir, "aria2.conf")
	p.DHT = filepath.Join(p.StateDir, "dht.dat")
	for _, d := range []string{p.StateDir, p.RuntimeDir, p.DownloadDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	s.Downloads.Dir = p.DownloadDir
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := engine.NewAria2(supervisor.New(s, p, log))
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	d := &liveDaemon{m: NewManager(e, st, s, NewBus(512), log), e: e, done: make(chan struct{})}
	if setup != nil {
		setup(d.m)
	}
	var runCtx context.Context
	runCtx, d.cancel = context.WithCancel(bg)
	go func() { d.m.Run(runCtx); close(d.done) }()
	return d
}

func (d *liveDaemon) stop(t *testing.T) {
	t.Helper()
	d.cancel()
	<-d.done
	ctx, cancel := context.WithTimeout(bg, 15*time.Second)
	defer cancel()
	if err := d.e.Shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func waitJob(t *testing.T, st *store.Store, id string, d time.Duration, ok func(*store.Job) bool) *store.Job {
	t.Helper()
	end := time.Now().Add(d)
	for {
		j, err := st.GetJob(bg, id)
		if err != nil {
			t.Fatal(err)
		}
		if ok(j) {
			return j
		}
		if time.Now().After(end) {
			t.Fatalf("job never matched; last state %s kind %v msg %v", j.State, j.ErrorKind, j.ErrorMsg)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A download with no size and no ranges has no control file, so a restart
// starts it over in its emptied staging folder; the download folder sees
// nothing until the one complete file appears.
func TestUnknownSize_NoPrecheck_RestartInStaging(t *testing.T) {
	testutil.RequireAria2(t)
	data := testutil.RandBytes(2 << 20)
	srv := testutil.NewFaultServer(t, map[string][]byte{"norange.bin": data})
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	slow := func(s *config.Settings) { s.Limits.GlobalDown = "512K" }

	// Any known size would be refused, so finishing proves no check ran.
	tiny := func(m *Manager) { m.free = func(string) (uint64, error) { return 1, nil } }
	d := startLive(t, st, root, slow, tiny)
	j, err := d.m.Add(bg, AddParams{URIs: []string{srv.URL("norange.bin", testutil.ModeNoRange)}})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, st, j.ID, 10*time.Second, func(j *store.Job) bool { return srv.Served("norange.bin") > 256<<10 })
	if got, _ := st.GetJob(bg, j.ID); got.State == store.Paused || got.State == store.Done {
		t.Fatalf("state %s before restart", got.State)
	}
	dl := filepath.Join(root, "dl")
	if names := dirNames(t, dl); len(names) != 1 || names[0] != ".laria" {
		t.Fatalf("download folder holds %v while downloading", names)
	}
	d.stop(t)

	d = startLive(t, st, root, nil, tiny)
	defer d.stop(t)
	got := waitJob(t, st, j.ID, 30*time.Second, func(j *store.Job) bool { return j.State == store.Done || j.State == store.Error })
	if got.State != store.Done {
		t.Fatalf("state %s kind %v msg %v", got.State, got.ErrorKind, got.ErrorMsg)
	}
	if !eventSeen(t, st, j.ID, "readded") {
		t.Fatal("restart did not re-add the job")
	}
	if n := srv.Served("norange.bin"); n <= int64(len(data)) {
		t.Fatalf("served %d bytes; the download did not start over", n)
	}
	if *got.Out != "norange.bin" || got.Staged {
		t.Fatalf("finished as %s (staged %v), want norange.bin published", *got.Out, got.Staged)
	}
	if names := dirNames(t, dl); len(names) != 1 || names[0] != "norange.bin" {
		t.Fatalf("download folder holds %v, want only norange.bin", names)
	}
	path := filepath.Join(dl, *got.Out)
	want := testutil.SHA256File(t, writeFile(t, data))
	if testutil.SHA256File(t, path) != want {
		t.Fatalf("sha mismatch for %s", path)
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func eventSeen(t *testing.T, st *store.Store, id, kind string) bool {
	t.Helper()
	evs, err := st.Events(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A file that appears between the on_exists check and aria2's start, under
// a name taken from the URL (no --out), is never written into: aria2 works
// in the job's staging folder and publish picks the next free name.
func TestFreshAddNeverOverwritesLateFile(t *testing.T) {
	testutil.RequireAria2(t)
	data := testutil.RandBytes(256 << 10)
	srv := testutil.NewFaultServer(t, map[string][]byte{"late.bin": data})
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := []byte("the user's own file\n")
	orig := filepath.Join(root, "dl", "late.bin")
	var once sync.Once
	spy := &addSpy{}
	late := func(m *Manager) {
		spy.Engine, m.e = m.e, spy
		dev := m.dev
		m.dev = func(dir string) (uint64, error) { // runs after the check, before the engine add
			once.Do(func() {
				if err := os.WriteFile(orig, old, 0o600); err != nil {
					t.Error(err)
				}
			})
			return dev(dir)
		}
	}
	d := startLive(t, st, root, nil, late)
	defer d.stop(t)
	j, err := d.m.Add(bg, AddParams{URIs: []string{srv.URL("late.bin", testutil.ModeOK)}})
	if err != nil {
		t.Fatal(err)
	}
	if out := spy.first().Out; out != "" {
		t.Fatalf("out %q sent by the daemon", out)
	}
	got := waitJob(t, st, j.ID, 30*time.Second, func(j *store.Job) bool { return j.State == store.Done || j.State == store.Error })
	if got.State != store.Done {
		t.Fatalf("state %s kind %v msg %v", got.State, got.ErrorKind, got.ErrorMsg)
	}
	if got.Name != "late.1.bin" || got.Out == nil || *got.Out != got.Name || got.Staged {
		t.Fatalf("name %q out %v; want late.1.bin published beside the user's file", got.Name, got.Out)
	}
	if b, _ := os.ReadFile(orig); string(b) != string(old) {
		t.Fatalf("the existing file was changed (now %d bytes)", len(b))
	}
	if testutil.SHA256File(t, filepath.Join(workDir(got), got.Name)) != testutil.SHA256File(t, writeFile(t, data)) {
		t.Fatal("sha mismatch")
	}
}

// addSpy records the engine add requests.
type addSpy struct {
	engine.Engine
	mu   sync.Mutex
	reqs []engine.AddRequest
}

func (s *addSpy) Add(ctx context.Context, req engine.AddRequest) (engine.EngineRef, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	return s.Engine.Add(ctx, req)
}

func (s *addSpy) first() engine.AddRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reqs[0]
}

// A server-named job whose first attempt failed before any response never
// learned its path; when the retry names an existing user file, the re-add
// must rename beside it, not resume into it.
func TestRetryNeverResumesIntoUnownedFile(t *testing.T) {
	testutil.RequireAria2(t)
	data := testutil.RandBytes(256 << 10)
	srv, hits := reportServer(t, data)
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := []byte("the user's own report\n")
	orig := filepath.Join(root, "dl", "report.pdf")
	retry := func(s *config.Settings) {
		s.Retry.MaxTries = 1 // aria2 gives up at once; the daemon's retry re-adds
		s.Retry.AutoRetry = []string{"503"}
		s.Retry.BackoffBase = "1s"
	}
	d := startLive(t, st, root, retry, func(*Manager) {
		if err := os.WriteFile(orig, old, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	defer d.stop(t)
	j, err := d.m.Add(bg, AddParams{URIs: []string{srv.URL + "/get"}})
	if err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, st, j.ID, 30*time.Second, func(j *store.Job) bool { return j.State == store.Done })
	if hits.Load() < 2 || !eventSeen(t, st, j.ID, "retry") {
		t.Fatalf("no re-add happened (%d requests)", hits.Load())
	}
	if b, _ := os.ReadFile(orig); string(b) != string(old) {
		t.Fatalf("the existing file was changed (now %d bytes)", len(b))
	}
	if got.Name != "report.1.pdf" || got.Out == nil || *got.Out != got.Name || got.Staged {
		t.Fatalf("name %q out %v; want report.1.pdf published beside the user's file", got.Name, got.Out)
	}
	if testutil.SHA256File(t, filepath.Join(workDir(got), got.Name)) != testutil.SHA256File(t, writeFile(t, data)) {
		t.Fatal("sha mismatch")
	}
}
