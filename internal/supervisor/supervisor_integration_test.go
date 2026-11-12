package supervisor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
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
	waitGone(t, pid, 5*time.Second)
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

// aria2.shutdown waits a fixed 3 s before halting; the daemon's idle exit
// must not (spec Q2: gone within 5 s of going idle with a 2 s timer).
func TestShutdownIsQuick(t *testing.T) {
	sv, _ := startSupervisor(t)
	start := time.Now()
	if err := sv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("Shutdown took %s, want under 2.5s", d)
	}
}

// Freeze hands over a live aria2c: supervision stops, the child keeps
// running and answering, and a later Shutdown still stops it.
func TestFreezeKeepsAria2(t *testing.T) {
	sv, _ := startSupervisor(t)
	pid := sv.pid()
	ep, ok := sv.Freeze()
	if !ok || ep.PID != pid || ep.Port == 0 || ep.Secret == "" {
		t.Fatalf("Freeze = %+v, %v; want the running aria2c %d", ep, ok, pid)
	}
	time.Sleep(500 * time.Millisecond)
	if !alive(pid) {
		t.Fatal("Freeze stopped aria2c")
	}
	c, err := aria2.Dial(context.Background(), fmt.Sprintf("ws://127.0.0.1:%d/jsonrpc", ep.Port), ep.Secret, aria2.Options{})
	if err != nil {
		t.Fatal(err)
	}
	getVersion(t, c)
	c.Close()
	if err := sv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitGone(t, pid, 3*time.Second)
}
