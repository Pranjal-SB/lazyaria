package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// fakeEngine keeps downloads in a map and records every call.
type fakeEngine struct {
	mu          sync.Mutex
	next        int
	dl          map[engine.EngineRef]engine.EngineStatus
	details     map[engine.EngineRef]engine.EngineDetails
	adds        []engine.AddRequest
	removed     []engine.EngineRef
	paused      []engine.EngineRef
	resumed     []engine.EngineRef
	statusCalls int
	addEntered  chan struct{} // receives once per Add when set
	addBlock    chan struct{} // Add waits on it when set
	onAdd       func(ref engine.EngineRef)
	changeRef   engine.EngineRef // ChangeURI result when set
	changeErr   error
	events      chan engine.EngineEvent
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		dl:      map[engine.EngineRef]engine.EngineStatus{},
		details: map[engine.EngineRef]engine.EngineDetails{},
		events:  make(chan engine.EngineEvent, 64),
	}
}

func (f *fakeEngine) Start(context.Context) error { return nil }

func (f *fakeEngine) Add(ctx context.Context, req engine.AddRequest) (engine.EngineRef, error) {
	if f.addEntered != nil {
		f.addEntered <- struct{}{}
	}
	if f.addBlock != nil {
		select {
		case <-f.addBlock:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f.mu.Lock()
	f.next++
	ref := engine.EngineRef(req.GID)
	if _, taken := f.dl[ref]; taken || ref == "" {
		ref = engine.EngineRef(fmt.Sprintf("g%d", f.next)) // like aria2 refusing the gid
	}
	f.adds = append(f.adds, req)
	st := "waiting"
	if req.Paused {
		st = "paused"
	}
	f.dl[ref] = engine.EngineStatus{Ref: ref, State: st, Known: true, IsBT: len(req.Torrent) > 0}
	f.mu.Unlock()
	if f.onAdd != nil {
		f.onAdd(ref)
	}
	return ref, nil
}

func (f *fakeEngine) set(ref engine.EngineRef, fn func(*engine.EngineStatus)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.dl[ref]
	st.Ref, st.Known = ref, true
	fn(&st)
	f.dl[ref] = st
}

func (f *fakeEngine) setState(ref engine.EngineRef, state string) {
	f.set(ref, func(s *engine.EngineStatus) { s.State = state })
}

func (f *fakeEngine) Pause(_ context.Context, ref engine.EngineRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, ref)
	if st, ok := f.dl[ref]; ok {
		st.State = "paused"
		f.dl[ref] = st
	}
	return nil
}

func (f *fakeEngine) Resume(_ context.Context, ref engine.EngineRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = append(f.resumed, ref)
	st, ok := f.dl[ref]
	if !ok || st.State != "paused" {
		return fmt.Errorf("cannot unpause %s", ref)
	}
	st.State = "waiting"
	f.dl[ref] = st
	return nil
}

func (f *fakeEngine) Remove(_ context.Context, ref engine.EngineRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, ref)
	delete(f.dl, ref)
	return nil
}

func (f *fakeEngine) ChangeURI(_ context.Context, ref engine.EngineRef, _ string) (engine.EngineRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.changeErr != nil {
		return "", f.changeErr
	}
	if f.changeRef != "" {
		f.dl[f.changeRef] = engine.EngineStatus{Ref: f.changeRef, State: "waiting", Known: true}
		delete(f.dl, ref)
		return f.changeRef, nil
	}
	return ref, nil
}

func (f *fakeEngine) Status(_ context.Context, refs []engine.EngineRef) ([]engine.EngineStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusCalls++
	out := make([]engine.EngineStatus, len(refs))
	for i, r := range refs {
		if st, ok := f.dl[r]; ok {
			out[i] = st
		} else {
			out[i] = engine.EngineStatus{Ref: r, State: "removed"}
		}
	}
	return out, nil
}

func (f *fakeEngine) List(context.Context) ([]engine.EngineStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []engine.EngineStatus
	for _, st := range f.dl {
		out = append(out, st)
	}
	return out, nil
}

func (f *fakeEngine) Details(_ context.Context, ref engine.EngineRef) (engine.EngineDetails, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.details[ref], nil
}

func (f *fakeEngine) SetGlobalLimit(context.Context, int64, int64) error             { return nil }
func (f *fakeEngine) SetLimit(context.Context, engine.EngineRef, int64, int64) error { return nil }
func (f *fakeEngine) Events() <-chan engine.EngineEvent                              { return f.events }
func (f *fakeEngine) Shutdown(context.Context) error                                 { return nil }

func (f *fakeEngine) count() (adds, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.adds), f.statusCalls
}

// clock is a settable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	m   *Manager
	e   *fakeEngine
	st  *store.Store
	bus *Bus
	clk *clock
	dir string
}

var bg = context.Background()

func newRig(t *testing.T) *rig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, _, _, err := config.Load("", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{e: newFakeEngine(), st: st, bus: NewBus(512), clk: &clock{t: time.UnixMilli(1_800_000_000_000)}, dir: t.TempDir()}
	r.m = NewManager(r.e, st, s, r.bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.m.now = r.clk.now
	r.m.free = func(string) (uint64, error) { return 1 << 50, nil }
	r.m.dev = func(string) (uint64, error) { return 7, nil }
	r.m.alloc = func(string) string { return "falloc" }
	r.m.rnd = func() float64 { return 0.5 } // no jitter
	t.Cleanup(r.m.stopRetries)
	return r
}

func (r *rig) add(t *testing.T, p AddParams) *store.Job {
	t.Helper()
	if p.Dir == "" {
		p.Dir = r.dir
	}
	j, err := r.m.Add(bg, p)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (r *rig) get(t *testing.T, id string) *store.Job {
	t.Helper()
	j, err := r.st.GetJob(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (r *rig) ref(t *testing.T, id string) engine.EngineRef {
	t.Helper()
	j := r.get(t, id)
	if j.EngineRef == nil {
		t.Fatal("job has no engine ref")
	}
	return engine.EngineRef(*j.EngineRef)
}

// event feeds one engine event straight to the handler.
func (r *rig) event(ref engine.EngineRef, kind string) {
	r.m.handleEvent(bg, engine.EngineEvent{Ref: ref, Kind: kind})
}

func (r *rig) hasEvent(t *testing.T, id, kind string) bool {
	t.Helper()
	evs, err := r.st.Events(bg, id)
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

// insert stores a job row directly, as if written by an earlier daemon.
func (r *rig) insert(t *testing.T, state store.State, ref string, mod func(*store.Job)) *store.Job {
	t.Helper()
	j := &store.Job{ID: store.NewID(), Kind: "http", Engine: "aria2", State: state, Name: "f.bin",
		URIs: []string{"http://example.com/f.bin"}, Dir: r.dir, Options: map[string]string{},
		Queue: "main", CreatedAt: r.clk.now()}
	if ref != "" {
		j.EngineRef = &ref
	}
	dev := uint64(7)
	j.DirDev = &dev
	if mod != nil {
		mod(j)
	}
	if err := r.st.InsertJob(bg, j); err != nil {
		t.Fatal(err)
	}
	return j
}
