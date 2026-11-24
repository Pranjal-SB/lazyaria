package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// fakeDaemon serves a fixed snapshot. Closing ev or fr is the daemon
// going away.
type fakeDaemon struct {
	snap  ipc.SnapshotResult
	ev    chan daemon.Event
	fr    chan daemon.Frame
	pause func(ids []string) // nil: record only

	mu     sync.Mutex
	paused []string
	closed bool
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

func (f *fakeDaemon) Add(context.Context, daemon.AddParams) (*store.Job, error) { return nil, nil }

func (f *fakeDaemon) Pause(_ context.Context, ids ...string) error {
	if f.pause != nil {
		f.pause(ids)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, ids...)
	return nil
}

func (f *fakeDaemon) Resume(context.Context, ...string) error { return nil }
func (f *fakeDaemon) Remove(context.Context, []string, bool) (string, []string, error) {
	return "", nil, nil
}
func (f *fakeDaemon) Undo(context.Context, string) error { return nil }
func (f *fakeDaemon) Details(context.Context, string, int) (daemon.Details, error) {
	return daemon.Details{}, nil
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
func (panicDaemon) Close() error { panic("Close called") }

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

func waitTrue(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}
