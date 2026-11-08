package supervisor

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// fakeAria2 writes a script that passes the version check and then runs body.
func fakeAria2(t *testing.T, body string) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "aria2c")
	src := "#!/bin/sh\n[ \"$1\" = --version ] && { echo 'aria2 version 1.37.0'; exit 0; }\n" + body
	if err := os.WriteFile(script, []byte(src), 0o700); err != nil {
		t.Fatal(err)
	}
	return script
}

func waitPhase(t *testing.T, sv *Supervisor, phase string, within time.Duration) EngineState {
	t.Helper()
	timeout := time.After(within)
	for {
		select {
		case st, ok := <-sv.State():
			if !ok {
				t.Fatalf("state channel closed while waiting for %q", phase)
			}
			if st.Phase == phase {
				return st
			}
		case <-timeout:
			t.Fatalf("no %q state within %v", phase, within)
		}
	}
}

func shutdownOnCleanup(t *testing.T, sv *Supervisor) {
	t.Cleanup(func() {
		if err := sv.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
}

func TestCrashLoopFails(t *testing.T) {
	s, p := testSettings(t)
	s.Engine.Aria2cPath = fakeAria2(t, "echo 'boom: cannot start' >&2\nexit 1\n")
	s.Engine.RestartMaxBackoff = "50ms"
	sv := New(s, p, quiet)
	shutdownOnCleanup(t, sv)
	_, err := sv.Start(context.Background())
	if !errors.Is(err, ErrEngineFailed) || !strings.Contains(err.Error(), "aria2.log") {
		t.Fatalf("Start err = %v, want ErrEngineFailed naming the log", err)
	}
	st := waitPhase(t, sv, "failed", time.Second)
	if !strings.Contains(strings.Join(st.LogTail, "\n"), "boom") {
		t.Fatalf("log tail %q lacks the script output", st.LogTail)
	}
}

func TestNeverReadyFails(t *testing.T) {
	s, p := testSettings(t)
	s.Engine.Aria2cPath = fakeAria2(t, "exec sleep 1000\n")
	s.Engine.RestartMaxBackoff = "50ms"
	sv := New(s, p, quiet)
	sv.readyTimeout = 200 * time.Millisecond
	shutdownOnCleanup(t, sv)
	done := make(chan error, 1)
	go func() {
		_, err := sv.Start(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrEngineFailed) {
			t.Fatalf("Start err = %v, want ErrEngineFailed", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start still blocked on an aria2c that never answers")
	}
}

func TestRestartAfterFailed(t *testing.T) {
	aria2Path := testutil.RequireAria2(t)
	s, p := testSettings(t)
	s.Engine.Aria2cPath = fakeAria2(t, "exit 1\n")
	s.Engine.RestartMaxBackoff = "50ms"
	sv := New(s, p, quiet)
	shutdownOnCleanup(t, sv)
	if _, err := sv.Start(context.Background()); !errors.Is(err, ErrEngineFailed) {
		t.Fatalf("Start err = %v", err)
	}
	waitPhase(t, sv, "failed", time.Second)
	s.Engine.Aria2cPath = aria2Path
	if err := sv.Restart(); err != nil {
		t.Fatal(err)
	}
	waitPhase(t, sv, "starting", time.Second)
	waitPhase(t, sv, "ready", 5*time.Second)
	select {
	case c := <-sv.Clients():
		getVersion(t, c)
	default:
		t.Fatal("no client published after Restart")
	}
	if err := sv.Restart(); err == nil {
		t.Fatal("Restart accepted while the engine is ready")
	}
}

// A process already on the port must never see our secret, and the stolen
// port must not count toward the crash loop.
func TestBindRaceNoSecretLeak(t *testing.T) {
	testutil.RequireAria2(t)
	if runtime.GOOS != "linux" {
		t.Skip("socket ownership check is Linux only")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []byte
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(conn)
			mu.Lock()
			got = append(got, b...)
			mu.Unlock()
			conn.Close()
		}
	}()
	t.Cleanup(func() { l.Close(); wg.Wait() })

	s, p := testSettings(t)
	sv := New(s, p, quiet)
	shutdownOnCleanup(t, sv)
	calls := 0
	sv.pickPort = func() (int, error) {
		calls++
		if calls == 1 {
			return l.Addr().(*net.TCPAddr).Port, nil
		}
		return freePort()
	}
	c, err := sv.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	getVersion(t, c)
	if calls < 2 {
		t.Fatal("never retried on a new port")
	}
	if sv.policy.fast != 0 {
		t.Fatal("bind race counted toward the crash loop")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("squatter on the port received %d bytes: %q", len(got), got)
	}
}
