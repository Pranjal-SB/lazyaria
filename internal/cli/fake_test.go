package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// fake is an in-process daemon behind a real socket.
type fake struct {
	bus *daemon.Bus

	mu        sync.Mutex
	jobs      []*store.Job
	adds      []daemon.AddParams
	addErr    error
	paused    []string
	removed   []string
	deleted   bool
	refreshed []string // id, link of each RefreshURI
	undone    []string // tokens passed to Undo
	overrides map[string]string
	restarts  int

	pid        int   // reported by Status
	restartErr error // returned by RestartEngine
	dropAdded  bool  // Add answers but keeps no row, as if removed at once
	version    string
	upgrades   int
	snapLimit  int      // limit of the last Snapshot
	snapIDs    []string // ids of the last Snapshot
	hangups    int      // Snapshot and Add calls that hang up, as a daemon re-executing itself does
	newPID     int      // when set, the daemon after a hang-up is another process
	stop       func()
	restart    func(pid int)
}

func (f *fake) All() (map[string]string, error) { return f.overrides, nil }

func (f *fake) Hello(context.Context, ipc.HelloParams) (ipc.HelloResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.version
	if v == "" {
		v = "dev"
	}
	return ipc.HelloResult{DaemonVersion: v, Protocol: 1, EngineState: "ready"}, nil
}

func (f *fake) Status(context.Context) (ipc.Status, error) {
	return ipc.Status{PID: f.pid, Mode: "on-demand", Uptime: 75, EngineState: "ready", Aria2Version: "1.37.0", DownloadDir: "/dl"}, nil
}

// Upgrade "re-executes" the same old binary: nothing changes.
func (f *fake) Upgrade(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upgrades++
	return nil
}

func (f *fake) RestartEngine(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return f.restartErr
}

func (f *fake) Add(ctx context.Context, p daemon.AddParams) (*store.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adds = append(f.adds, p)
	if f.hangup(ctx) {
		return nil, nil
	}
	if f.addErr != nil {
		if sk, ok := errors.AsType[*daemon.SkippedError](f.addErr); ok {
			return sk.Existing, f.addErr
		}
		return nil, f.addErr
	}
	j := &store.Job{ID: store.NewID(), Kind: "http", State: store.Queued, URIs: p.URIs, Name: "file", Dir: p.Dir}
	if !f.dropAdded {
		f.jobs = append(f.jobs, j)
	}
	return j, nil
}

func (f *fake) Pause(_ context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, ids...)
	return nil
}

func (f *fake) Resume(context.Context, []string) error { return nil }
func (f *fake) PauseAll(context.Context) error         { return nil }
func (f *fake) ResumeAll(context.Context) error        { return nil }

func (f *fake) Remove(_ context.Context, ids []string, del bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed, f.deleted = append(f.removed, ids...), del
	return "tok", nil
}

func (f *fake) Undo(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.undone = append(f.undone, token)
	return nil
}

func (f *fake) RefreshURI(_ context.Context, id, uri string) (*store.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshed = append(f.refreshed, id, uri)
	return nil, nil
}
func (f *fake) SetLimit(context.Context, string, int64, int64) error { return nil }
func (f *fake) StopSeeding(context.Context, []string) error          { return nil }

func (f *fake) Snapshot(ctx context.Context, _ bool, limit int, ids []string) (uint64, []*store.Job, []daemon.SpeedSample) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapLimit, f.snapIDs = limit, ids
	if f.hangup(ctx) {
		return 0, nil, nil
	}
	js := make([]*store.Job, len(f.jobs))
	for i, j := range f.jobs {
		c := *j
		js[i] = &c
	}
	return 0, js, nil
}

// hangup cuts the current call off, as a daemon re-executing itself does,
// while hangups are left. f.mu must be held.
func (f *fake) hangup(ctx context.Context) bool {
	if f.hangups == 0 {
		return false
	}
	f.hangups--
	go f.restart(f.newPID)
	f.mu.Unlock()
	<-ctx.Done()
	f.mu.Lock()
	return true
}

func (f *fake) Search(context.Context, string, int, int) ([]*store.Job, error) { return nil, nil }
func (f *fake) Subscribe(progress bool) *daemon.Sub                            { return f.bus.Subscribe(progress) }
func (f *fake) SetObservers(int)                                               {}
func (f *fake) SetClients(int)                                                 {}

func (f *fake) ConfigList(context.Context) ([]config.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ents, _, err := config.Load("", nil, f, nil)
	return ents, err
}

func (f *fake) ConfigSet(_ context.Context, key, value string) error {
	if err := config.Set(&config.Settings{Keys: map[string][]string{}, Theme: map[string]string{}}, key, value); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides[key] = value
	return nil
}

func (f *fake) ConfigReset(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.overrides, key)
	return nil
}

// isolate points every XDG dir at a fresh short temp dir (sockets have a
// 104-byte path limit) and turns daemon auto-start off.
func isolate(t *testing.T) config.Paths {
	t.Helper()
	root, err := os.MkdirTemp("", "lac")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "c"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "s"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "r"))
	t.Setenv("LAZYARIA_DAEMON_AUTO_START", "false")
	_, p, err := resolveLocal(os.Environ(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// startFake serves a fake daemon where the CLI will look for one.
func startFake(t *testing.T) *fake {
	t.Helper()
	p := isolate(t)
	f := &fake{bus: daemon.NewBus(0), overrides: map[string]string{}}
	serve := func(pid int) func() {
		l, lock, err := ipc.Listen(p)
		if err != nil {
			t.Error(err)
			return func() {}
		}
		if pid != 0 {
			os.WriteFile(p.Lock, []byte(strconv.Itoa(pid)+"\n"), 0o600)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { ipc.Serve(ctx, l, f); close(done) }()
		return func() { cancel(); <-done; lock.Close() }
	}
	var mu sync.Mutex
	stop := serve(0)
	// restart hangs up on every client and serves again, as the same pid
	// would after an exec, or as pid when it is set.
	f.restart = func(pid int) {
		mu.Lock()
		defer mu.Unlock()
		stop()
		stop = serve(pid)
	}
	var once sync.Once
	f.stop = func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			stop()
		})
	}
	t.Cleanup(f.stop)
	return f
}

// run calls a command with in as its stdin.
func run(t *testing.T, cmd func(context.Context, []string, io.Writer, io.Writer) int, in string, args ...string) (int, string, string) {
	t.Helper()
	old := stdin
	stdin = strings.NewReader(in)
	t.Cleanup(func() { stdin = old })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	code := cmd(ctx, args, &out, &errOut)
	return code, out.String(), errOut.String()
}
