//go:build linux

package cli_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lazyaria-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin = filepath.Join(dir, "lazyaria")
	out, err := exec.Command("go", "build", "-o", bin, "../..").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build lazyaria: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

var bg = context.Background()

// env is one isolated XDG home with its own config file.
type env struct {
	t    *testing.T
	root string
	vars []string
	p    config.Paths
}

func newEnv(t *testing.T, cfg string) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, root: root}
	for _, d := range []string{"cfg/lazyaria", "state", "run", "dl"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	e.vars = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, "cfg"),
		"XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"XDG_RUNTIME_DIR=" + filepath.Join(root, "run"),
	}
	base := fmt.Sprintf("[downloads]\ndir = %q\n[bt]\nenable_dht = false\n", filepath.Join(root, "dl"))
	e.writeConfig(base + cfg)
	s, _, _, err := config.Load("", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.p, err = config.ResolvePaths(s, e.getenv); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.kill)
	return e
}

func (e *env) getenv(k string) string {
	for _, kv := range e.vars {
		if v, ok := strings.CutPrefix(kv, k+"="); ok {
			return v
		}
	}
	return ""
}

func (e *env) writeConfig(body string) {
	e.t.Helper()
	path := filepath.Join(e.root, "cfg", "lazyaria", "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) cmd(args ...string) *exec.Cmd {
	c := exec.Command(bin, args...)
	c.Env = e.vars
	return c
}

// detach runs `daemon --detach` and returns its exit code and output.
func (e *env) detach(args ...string) (int, string) {
	e.t.Helper()
	c := e.cmd(append([]string{"daemon", "--detach"}, args...)...)
	out, err := c.CombinedOutput()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode(), string(out)
	} else if err != nil {
		e.t.Fatal(err)
	}
	return 0, string(out)
}

func (e *env) start(args ...string) {
	e.t.Helper()
	if code, out := e.detach(args...); code != 0 {
		e.t.Fatalf("daemon --detach exited %d: %s", code, out)
	}
}

// pid reads the daemon pid from the lock file.
func (e *env) pid() int {
	b, _ := os.ReadFile(e.p.Lock)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

func (e *env) dial() *ipc.Client {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(bg, 5*time.Second)
	defer cancel()
	var c *ipc.Client
	var err error
	for c, err = ipc.Dial(ctx, e.p); err != nil; c, err = ipc.Dial(ctx, e.p) {
		if ctx.Err() != nil {
			e.t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Cleanup(func() { c.Close() })
	return c
}

// kill stops a daemon left running by a failed test, and shows its log.
func (e *env) kill() {
	if e.t.Failed() {
		b, _ := os.ReadFile(e.p.Log)
		e.t.Logf("daemon log:\n%s", b)
	}
	if pid := e.pid(); pid > 0 && alive(pid) {
		syscall.Kill(pid, syscall.SIGTERM)
		waitGone(pid, 15*time.Second)
	}
}

func alive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// A zombie has already exited; only its parent has not reaped it.
	f := strings.Fields(string(b[strings.LastIndexByte(string(b), ')')+1:]))
	return len(f) > 0 && f[0] != "Z"
}

func waitGone(pid int, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if !alive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return !alive(pid)
}

// aria2Child returns the pid of the daemon's aria2c child, or 0.
func aria2Child(t *testing.T, daemonPID int) int {
	t.Helper()
	ds, _ := os.ReadDir("/proc")
	for _, d := range ds {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		s := string(b)
		comm := s[strings.IndexByte(s, '(')+1 : strings.LastIndexByte(s, ')')]
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if comm == "aria2c" && len(f) > 1 && f[1] == strconv.Itoa(daemonPID) {
			return pid
		}
	}
	return 0
}

// aria2Client dials the daemon's aria2c with the port and secret from its conf file.
func (e *env) aria2Client() *aria2.Client {
	e.t.Helper()
	f, err := os.Open(e.p.AriaConf)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	var port, secret string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), "=")
		switch k {
		case "rpc-listen-port":
			port = v
		case "rpc-secret":
			secret = v
		}
	}
	ctx, cancel := context.WithTimeout(bg, 3*time.Second)
	defer cancel()
	c, err := aria2.Dial(ctx, "ws://127.0.0.1:"+port+"/jsonrpc", secret, aria2.Options{})
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { c.Close() })
	return c
}

func globalDown(t *testing.T, c *aria2.Client) string {
	t.Helper()
	var opts map[string]string
	if err := c.Call(bg, "aria2.getGlobalOption", nil, &opts); err != nil {
		t.Fatal(err)
	}
	return opts["max-overall-download-limit"]
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
