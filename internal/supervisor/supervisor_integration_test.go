package supervisor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

const helperEnv = "LAZYARIA_SUPERVISOR_HELPER_DIR"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func startSupervisor(t *testing.T) (*Supervisor, *aria2.Client) {
	t.Helper()
	testutil.RequireAria2(t)
	s, p := testSettings(t)
	sv := New(s, p, quiet)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := sv.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := sv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return sv, c
}

func (sv *Supervisor) pid() int {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return sv.cur.cmd.Process.Pid
}

func (sv *Supervisor) secret() string {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return sv.cur.secret
}

func getVersion(t *testing.T, c *aria2.Client) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var v struct{ Version string }
	if err := c.Call(ctx, "aria2.getVersion", nil, &v); err != nil {
		t.Fatalf("getVersion: %v", err)
	}
	return v.Version
}

// alive treats a zombie as gone: it has exited and is only waiting to be reaped.
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	_, rest, _ := strings.Cut(string(b), ") ")
	return !strings.HasPrefix(rest, "Z")
}

func waitGone(t *testing.T, pid int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still alive after %v", pid, within)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartReady(t *testing.T) {
	before := runtime.NumGoroutine()
	testutil.RequireAria2(t)
	s, p := testSettings(t)
	sv := New(s, p, quiet)
	c, err := sv.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v := getVersion(t, c); v == "" {
		t.Fatal("empty version")
	}
	pid := sv.pid()
	if err := sv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitGone(t, pid, time.Second)
	var last EngineState
	for st := range sv.State() {
		last = st
	}
	if last.Phase != "ready" {
		t.Fatalf("last phase %q, want ready", last.Phase)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines leaked: %d > %d\n%s", runtime.NumGoroutine(), before, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSecretNotInArgv(t *testing.T) {
	sv, _ := startSupervisor(t)
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", sv.pid()))
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	if strings.Contains(string(b), sv.secret()) || len(sv.secret()) != 32 {
		t.Fatalf("secret leaked or malformed; argv=%q", args)
	}
	want := []string{"--conf-path=" + sv.p.AriaConf, "--stop-with-process=" + strconv.Itoa(os.Getpid())}
	if len(args) != 3 || args[1] != want[0] || args[2] != want[1] {
		t.Fatalf("argv = %q, want [aria2c %q]", args, want)
	}
}

func TestKill9Respawn(t *testing.T) {
	sv, _ := startSupervisor(t)
	for len(sv.State()) > 0 {
		<-sv.State()
	}
	old := sv.pid()
	if err := syscall.Kill(old, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	sawRestarting := false
	for done := false; !done; {
		select {
		case st := <-sv.State():
			sawRestarting = sawRestarting || st.Phase == "restarting"
			done = st.Phase == "ready"
		case <-timeout:
			t.Fatal("not ready again within 5s")
		}
	}
	if !sawRestarting {
		t.Fatal("never saw the restarting phase")
	}
	select {
	case c := <-sv.Clients():
		getVersion(t, c)
	default:
		t.Fatal("no new client published")
	}
	if sv.pid() == old {
		t.Fatal("pid unchanged after respawn")
	}
}

func TestCrashLoopFails(t *testing.T) {
	s, p := testSettings(t)
	script := filepath.Join(t.TempDir(), "aria2c")
	body := "#!/bin/sh\n[ \"$1\" = --version ] && { echo 'aria2 version 1.37.0'; exit 0; }\necho 'boom: cannot start' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	s.Engine.Aria2cPath = script
	s.Engine.RestartMaxBackoff = "50ms"
	sv := New(s, p, quiet)
	if _, err := sv.Start(context.Background()); err == nil {
		t.Fatal("Start succeeded on a crash loop")
	}
	var last EngineState
	for st := range sv.State() {
		last = st
	}
	if last.Phase != "failed" || len(last.LogTail) == 0 || !strings.Contains(strings.Join(last.LogTail, "\n"), "boom") {
		t.Fatalf("last state %+v, want failed with the log tail", last)
	}
	if err := sv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBindRaceRetries(t *testing.T) {
	testutil.RequireAria2(t)
	s, p := testSettings(t)
	sv := New(s, p, quiet)
	taken := 0
	sv.pickPort = func() (int, error) {
		if taken == 0 {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return 0, err
			}
			t.Cleanup(func() { l.Close() })
			taken = 1
			return l.Addr().(*net.TCPAddr).Port, nil
		}
		return freePort()
	}
	c, err := sv.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sv.Shutdown(context.Background())
	getVersion(t, c)
	if sv.policy.fast != 0 {
		t.Fatal("bind race counted toward the crash loop")
	}
}

func TestDiesWithParent(t *testing.T) {
	testutil.RequireAria2(t)
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperSupervisor$")
	cmd.Env = append(os.Environ(), helperEnv+"="+dir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("helper output %q", out)
	}
	waitGone(t, pid, 2*time.Second)
}

// TestHelperSupervisor is not a real test: TestDiesWithParent re-runs the
// binary with helperEnv set, and this starts aria2 and exits without cleanup.
func TestHelperSupervisor(t *testing.T) {
	dir := os.Getenv(helperEnv)
	if dir == "" {
		t.Skip("helper process only")
	}
	s, p := settingsIn(t, dir)
	sv := New(s, p, quiet)
	if _, err := sv.Start(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(sv.pid())
	os.Exit(0)
}
