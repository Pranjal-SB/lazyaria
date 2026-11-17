package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestMain(m *testing.M) {
	switch {
	case os.Getenv("LAZYARIA_TEST_FAKE_DAEMON") == "fail":
		fmt.Fprintln(os.Stderr, "aria2c not found; install it with: sudo pacman -S aria2")
		os.Exit(1)
	case os.Getenv("LAZYARIA_TEST_FAKE_DAEMON") == "1":
		os.Exit(fakeDaemon(os.Getenv("LAZYARIA_TEST_DIR")))
	case os.Getenv("LAZYARIA_TEST_DIAL") != "":
		os.Exit(dialAndRead(os.Getenv("LAZYARIA_TEST_DIAL")))
	}
	os.Exit(m.Run())
}

// fakeDaemon stands in for `lazyaria daemon --detach` until the real
// command exists. It records the outcome in dir/log. Like the real one it
// exits 0 when another daemon already holds the lock.
func fakeDaemon(dir string) int {
	p := testPaths(dir)
	l, lock, err := Listen(p)
	if err != nil {
		appendLog(dir, "busy")
		if _, ok := errors.AsType[*ErrAlreadyRunning](err); ok {
			return 0
		}
		return 1
	}
	defer lock.Close()
	appendLog(dir, "listening")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := Serve(ctx, l, newFake()); err != nil {
		return 1
	}
	return 0
}

func appendLog(dir, line string) {
	f, err := os.OpenFile(filepath.Join(dir, "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(line + "\n")
}

// dialAndRead exits 0 when the server hangs up on us, 2 when it keeps the
// connection open.
func dialAndRead(sock string) int {
	c, err := net.Dial("unix", sock)
	if err != nil {
		return 1
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = c.Read(make([]byte, 1))
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return 2
	}
	return 0
}

func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "la") // t.TempDir can exceed the 104-byte socket limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func testPaths(dir string) config.Paths {
	run := filepath.Join(dir, "run")
	return config.Paths{StateDir: dir, RuntimeDir: run, Socket: filepath.Join(run, "s.sock"), Lock: filepath.Join(run, "daemon.lock")}
}

type fakeHandler struct {
	bus      *daemon.Bus
	version  string
	protocol int

	mu         sync.Mutex
	jobs       []*store.Job
	addErr     error
	upgradeErr error
	upgrades   int
	observers  []int
	clients    []int
}

func newFake() *fakeHandler {
	return &fakeHandler{bus: daemon.NewBus(0), version: "dev", protocol: 1}
}

func (f *fakeHandler) Hello(context.Context, HelloParams) (HelloResult, error) {
	return HelloResult{DaemonVersion: f.version, Protocol: f.protocol, EngineState: "running"}, nil
}

func (f *fakeHandler) Status(context.Context) (Status, error) {
	return Status{PID: os.Getpid(), Mode: "auto"}, nil
}

func (f *fakeHandler) Upgrade(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upgrades++
	return f.upgradeErr
}

func (f *fakeHandler) RestartEngine(context.Context) error { return nil }

func (f *fakeHandler) Add(_ context.Context, p daemon.AddParams) (*store.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		var sk *daemon.SkippedError
		if errors.As(f.addErr, &sk) {
			return sk.Existing, f.addErr
		}
		return nil, f.addErr
	}
	j := &store.Job{ID: "j" + string(rune('0'+len(f.jobs))), URIs: p.URIs, Options: p.Options, State: store.Queued}
	f.jobs = append(f.jobs, j)
	return j, nil
}

func (f *fakeHandler) Pause(context.Context, []string) error  { return nil }
func (f *fakeHandler) Resume(context.Context, []string) error { return nil }
func (f *fakeHandler) PauseAll(context.Context) error         { return nil }
func (f *fakeHandler) ResumeAll(context.Context) error        { return nil }
func (f *fakeHandler) Remove(context.Context, []string, bool) (string, []string, error) {
	return "tok", nil, nil
}
func (f *fakeHandler) Undo(context.Context, string) error { return nil }
func (f *fakeHandler) RefreshURI(context.Context, string, string) (*store.Job, error) {
	return nil, nil
}
func (f *fakeHandler) SetLimit(context.Context, string, int64, int64) error { return nil }
func (f *fakeHandler) StopSeeding(context.Context, []string) error          { return nil }

func (f *fakeHandler) Snapshot(context.Context, bool, int, []string, []string) (uint64, []*store.Job, []daemon.SpeedSample) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return 7, append([]*store.Job(nil), f.jobs...), nil
}

func (f *fakeHandler) Search(context.Context, string, int, int) ([]*store.Job, error) {
	return f.jobs, nil
}

func (f *fakeHandler) Subscribe(progress bool) *daemon.Sub { return f.bus.Subscribe(progress) }

func (f *fakeHandler) SetObservers(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observers = append(f.observers, n)
}

func (f *fakeHandler) SetClients(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clients = append(f.clients, n)
}

func (f *fakeHandler) ConfigList(context.Context) ([]config.Entry, error) {
	return []config.Entry{{Key: "downloads.dir", Value: "/x", Source: config.SourceOverride}}, nil
}
func (f *fakeHandler) ConfigSet(context.Context, string, string) error { return nil }
func (f *fakeHandler) ConfigReset(context.Context, string) error       { return nil }

// serve starts a server on a fresh socket and returns a connected client.
func serve(t *testing.T, h Handler) (*Client, config.Paths) {
	t.Helper()
	p := testPaths(shortDir(t))
	l, lock, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, h) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
		lock.Close()
	})
	c, err := Dial(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, p
}
