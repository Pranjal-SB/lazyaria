//go:build e2e && linux

// Package e2e drives the real lazyaria binary and the real aria2c. Every
// test gets its own HOME and XDG dirs, so a running daemon of the user's
// is never touched.
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lazyaria-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin = filepath.Join(dir, "lazyaria")
	// e2ecrash adds a crash hook that only an environment variable turns on.
	build := exec.Command("go", "build", "-tags", "e2ecrash", "-o", bin, ".")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build lazyaria: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// skipOrFail skips, except under CI or E2E_STRICT=1, where a test that
// cannot run is a failure: a green run must mean everything ran.
func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") != "" || os.Getenv("E2E_STRICT") == "1" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

// env is one isolated user: HOME, XDG dirs, config file and download dir.
type env struct {
	t    *testing.T
	root string
	dl   string
	rt   string
	vars []string
	p    config.Paths
}

func newEnv(t *testing.T, cfg string) *env {
	t.Helper()
	if _, err := exec.LookPath("aria2c"); err != nil {
		skipOrFail(t, "aria2c not found on PATH")
	}
	root := t.TempDir()
	// The socket path must stay under 104 bytes, and a long TMPDIR would
	// otherwise push lazyaria onto the shared /tmp/lazyaria-<uid> fallback.
	rt, err := os.MkdirTemp("/tmp", "la")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(rt) })
	e := &env{t: t, root: root, dl: filepath.Join(root, "dl"), rt: rt}
	for _, d := range []string{"cfg/lazyaria", "data", "state", "dl"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	e.vars = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + root,
		"XDG_CONFIG_HOME=" + filepath.Join(root, "cfg"),
		"XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"XDG_DATA_HOME=" + filepath.Join(root, "data"),
		"XDG_RUNTIME_DIR=" + rt,
	}
	base := fmt.Sprintf("[downloads]\ndir = %q\n", e.dl)
	path := filepath.Join(root, "cfg", "lazyaria", "config.toml")
	if err := os.WriteFile(path, []byte(base+cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if e.p, err = config.ResolvePaths(&config.Settings{}, e.getenv); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.p.Socket, rt) {
		t.Fatalf("socket %s is outside the test runtime dir %s", e.p.Socket, rt)
	}
	t.Cleanup(e.cleanup)
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

func (e *env) cmd(args ...string) *exec.Cmd {
	c := exec.Command(bin, args...)
	c.Env = e.vars
	return c
}

// run runs one CLI command and returns stdout, stderr and the exit code.
func (e *env) run(args ...string) (string, string, int) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, bin, args...)
	c.Env = e.vars
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	err := c.Run()
	code := 0
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatalf("lazyaria %v: %v", args, err)
	}
	return out.String(), errOut.String(), code
}

// ok runs a CLI command that must exit 0 and returns its stdout.
func (e *env) ok(args ...string) string {
	e.t.Helper()
	out, errOut, code := e.run(args...)
	if code != 0 {
		e.t.Fatalf("lazyaria %v exited %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	return out
}

func (e *env) jobs() []*store.Job {
	e.t.Helper()
	var doc struct {
		Jobs []*store.Job `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(e.ok("list", "--all", "--json")), &doc); err != nil {
		e.t.Fatal(err)
	}
	return doc.Jobs
}

func (e *env) job(id string) *store.Job {
	e.t.Helper()
	for _, j := range e.jobs() {
		if j.ID == id {
			return j
		}
	}
	e.t.Fatalf("job %s is not in the list", id)
	return nil
}

func (e *env) status() ipc.Status {
	e.t.Helper()
	var st ipc.Status
	if err := json.Unmarshal([]byte(e.ok("status", "--json")), &st); err != nil {
		e.t.Fatal(err)
	}
	return st
}

// add queues links and returns the new job ids, in order.
func (e *env) add(args ...string) []string {
	e.t.Helper()
	var ids []string
	for _, line := range strings.Split(e.ok(append([]string{"add"}, args...)...), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "added" {
			ids = append(ids, f[1])
		}
	}
	return ids
}

// waitState polls until the job reaches one of the states.
func (e *env) waitState(id string, d time.Duration, states ...store.State) *store.Job {
	e.t.Helper()
	var j *store.Job
	waitFor(e.t, d, fmt.Sprintf("job %s to reach %v", id, states), func() bool {
		j = e.job(id)
		for _, s := range states {
			if j.State == s {
				return true
			}
		}
		return false
	})
	return j
}

func (e *env) dial() *ipc.Client {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		c, err := ipc.Dial(ctx, e.p)
		if err == nil {
			e.t.Cleanup(func() { c.Close() })
			return c
		}
		if ctx.Err() != nil {
			e.t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for !ok() {
		if time.Now().After(end) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// payload returns n bytes unique to salt. RandBytes alone gives the same
// prefix for every size, which would hide a mix-up between files.
func payload(n int, salt byte) []byte {
	b := testutil.RandBytes(n)
	for i := range b {
		b[i] ^= salt
	}
	return b
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
