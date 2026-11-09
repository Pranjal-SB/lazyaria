//go:build linux

package cli_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

func TestDaemonAddCompleteIdleExit(t *testing.T) {
	testutil.RequireAria2(t)
	srv := testutil.NewFaultServer(t, map[string][]byte{"a.bin": testutil.RandBytes(256 << 10)})
	e := newEnv(t, "[daemon]\nidle_exit = \"2s\"\n")
	e.start()
	pid := e.pid()
	c := e.dial()
	j, err := c.Add(bg, daemon.AddParams{URIs: []string{srv.URL("a.bin", testutil.ModeOK)}})
	if err != nil {
		t.Fatal(err)
	}
	ariaPID := aria2Child(t, pid)
	if ariaPID == 0 {
		t.Fatal("no aria2c child found")
	}
	waitFor(t, 15*time.Second, "job done", func() bool {
		r, err := c.List(bg, true, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range r.Jobs {
			if x.ID == j.ID {
				return x.State == store.Done
			}
		}
		return false
	})
	c.Close()
	if !waitGone(pid, 5*time.Second) {
		t.Fatalf("daemon %d still running 5s after the last job finished", pid)
	}
	if alive(ariaPID) {
		t.Errorf("aria2c %d outlived the daemon", ariaPID)
	}
	if _, err := os.Stat(e.p.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket still there after idle exit: %v", err)
	}
}

func TestDaemonAlwaysModeStays(t *testing.T) {
	testutil.RequireAria2(t)
	e := newEnv(t, "[daemon]\nidle_exit = \"1s\"\n")
	e.start("--mode", "always")
	time.Sleep(5 * time.Second)
	if pid := e.pid(); !alive(pid) {
		t.Fatalf("always-mode daemon %d exited while idle", pid)
	}
}

func TestSIGTERMGraceful(t *testing.T) {
	testutil.RequireAria2(t)
	e := newEnv(t, "")
	cmd := e.cmd("daemon")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := e.dial()
	if _, err := c.Status(bg); err != nil {
		t.Fatal(err)
	}
	ariaPID := aria2Child(t, cmd.Process.Pid)
	cmd.Process.Signal(syscall.SIGTERM)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("daemon exit after SIGTERM: %v, want 0", err)
	}
	if ariaPID != 0 && alive(ariaPID) {
		t.Errorf("aria2c %d outlived the daemon", ariaPID)
	}
	st, err := store.Open(e.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSetting(bg, "ui.glyphs", "ascii"); err != nil {
		t.Errorf("database still locked after shutdown: %v", err)
	}
}

func TestSIGHUPReloadsLive(t *testing.T) {
	testutil.RequireAria2(t)
	e := newEnv(t, "")
	e.start()
	_ = e.dial()
	a := e.aria2Client()
	if got := globalDown(t, a); got != "0" {
		t.Fatalf("initial global limit = %q, want 0", got)
	}
	e.writeConfig(fmt.Sprintf("[downloads]\ndir = %q\n[bt]\nenable_dht = false\n[limits]\nglobal_down = \"1M\"\n", filepath.Join(e.root, "dl")))
	syscall.Kill(e.pid(), syscall.SIGHUP)
	waitFor(t, 3*time.Second, "global limit 1M", func() bool { return globalDown(t, a) == "1048576" })
}

func TestConfigSetLiveApplies(t *testing.T) {
	testutil.RequireAria2(t)
	e := newEnv(t, "")
	e.start()
	c := e.dial()
	a := e.aria2Client()
	if err := c.ConfigSet(bg, "limits.global_down", "1M"); err != nil {
		t.Fatal(err)
	}
	if got := globalDown(t, a); got != "1048576" {
		t.Errorf("aria2 global limit = %q right after config.set, want 1048576", got)
	}
	ent, err := c.ConfigGet(bg, "limits.global_down")
	if err != nil {
		t.Fatal(err)
	}
	if ent.Value != "1M" || ent.Source != "override" {
		t.Errorf("config.get = %+v, want 1M from override", ent)
	}
	if err := c.ConfigSet(bg, "limits.global_down", "lots"); err == nil {
		t.Error("config.set accepted a bad size")
	}
	pid := e.pid()
	syscall.Kill(pid, syscall.SIGTERM)
	if !waitGone(pid, 15*time.Second) {
		t.Fatal("daemon did not stop")
	}
	st, err := store.Open(e.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m, err := st.All()
	if err != nil {
		t.Fatal(err)
	}
	if m["limits.global_down"] != "1M" {
		t.Errorf("settings table = %v, want limits.global_down=1M", m)
	}
}

func TestDaemonAria2MissingMessage(t *testing.T) {
	e := newEnv(t, "[engine]\naria2c_path = \"/nonexistent\"\n")
	for _, kv := range e.vars {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	_, err := ipc.Connect(ctx, e.p, true, bin)
	if err == nil {
		t.Fatal("Connect succeeded without aria2c")
	}
	if msg := err.Error(); !strings.Contains(msg, "aria2c not found") || !strings.Contains(msg, aria2.InstallHint()) {
		t.Errorf("error %q lacks %q and the install hint %q", msg, "aria2c not found", aria2.InstallHint())
	}
}

func TestDetachLoserExitsZero(t *testing.T) {
	testutil.RequireAria2(t)
	e := newEnv(t, "")
	e.start()
	pid := e.pid()
	if code, out := e.detach(); code != 0 {
		t.Fatalf("second daemon --detach exited %d (%s), want 0", code, out)
	}
	if e.pid() != pid || !alive(pid) {
		t.Errorf("the first daemon was replaced")
	}
}

func TestUpgradeKeepsPidAndAria2(t *testing.T) {
	testutil.RequireAria2(t)
	e := newEnv(t, "")
	e.start()
	pid := e.pid()
	c := e.dial()
	ariaPID := aria2Child(t, pid)
	if ariaPID == 0 {
		t.Fatal("no aria2c child found")
	}
	if err := c.Upgrade(bg); err != nil {
		t.Fatalf("daemon.upgrade: %v", err)
	}
	c.Close()
	var st ipc.Status
	waitFor(t, 10*time.Second, "the upgraded daemon", func() bool {
		// The exec'd image's initial environment shows the exec happened.
		if b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid)); !strings.Contains(string(b), "LAZYARIA_UPGRADE=") {
			return false
		}
		ctx, cancel := context.WithTimeout(bg, time.Second)
		defer cancel()
		c2, err := ipc.Dial(ctx, e.p)
		if err != nil {
			return false
		}
		defer c2.Close()
		st, err = c2.Status(ctx)
		return err == nil && st.EngineState == "ready"
	})
	if st.PID != pid {
		t.Errorf("pid after upgrade = %d, want %d", st.PID, pid)
	}
	if got := aria2Child(t, pid); got != ariaPID {
		t.Errorf("aria2c pid after upgrade = %d, want %d (kept running)", got, ariaPID)
	}
	// The adopted aria2c is still supervised: kill it and a new one, which
	// must not see the handoff variable, takes its place.
	syscall.Kill(ariaPID, syscall.SIGKILL)
	var respawned int
	waitFor(t, 10*time.Second, "a respawned aria2c", func() bool {
		respawned = aria2Child(t, pid)
		return respawned != 0 && respawned != ariaPID && alive(respawned)
	})
	environ, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", respawned))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(environ), "LAZYARIA_UPGRADE") {
		t.Error("the respawned aria2c sees the upgrade handoff variable")
	}
}

// A path fixed for engine.restart must survive later live reloads, or the
// next crash respawn goes back to the broken one.
func TestEngineRestartKeepsFixedPath(t *testing.T) {
	good := testutil.RequireAria2(t)
	e := newEnv(t, "")
	if err := os.MkdirAll(filepath.Dir(e.p.DB), 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(e.p.DB)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSetting(bg, "engine.aria2c_path", "/nonexistent")
	st.Close()

	e.start()
	pid := e.pid()
	c := e.dial()
	engineIs := func(want string) func() bool {
		return func() bool { s, err := c.Status(bg); return err == nil && s.EngineState == want }
	}
	waitFor(t, 5*time.Second, "engine failed", engineIs("failed"))
	if err := c.ConfigSet(bg, "engine.aria2c_path", good); err != nil {
		t.Fatal(err)
	}
	if err := c.RestartEngine(bg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "engine ready", engineIs("ready"))
	if err := c.ConfigSet(bg, "limits.global_down", "1M"); err != nil {
		t.Fatal(err)
	}
	syscall.Kill(pid, syscall.SIGHUP)
	time.Sleep(200 * time.Millisecond)

	old := aria2Child(t, pid)
	syscall.Kill(old, syscall.SIGKILL)
	var respawned int
	waitFor(t, 10*time.Second, "a respawned aria2c", func() bool {
		respawned = aria2Child(t, pid)
		return respawned != 0 && respawned != old && alive(respawned)
	})
	b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", respawned))
	if argv0, _, _ := strings.Cut(string(b), "\x00"); argv0 != good {
		t.Errorf("respawned aria2c runs %q, want %q", argv0, good)
	}
	waitFor(t, 5*time.Second, "engine ready after respawn", engineIs("ready"))
}

// A second SIGTERM while shutting down exits at once; aria2c then follows
// through --stop-with-process.
func TestSecondSignalForcesExit(t *testing.T) {
	testutil.RequireAria2(t)
	e := newEnv(t, "")
	cmd := e.cmd("daemon")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := e.dial()
	if _, err := c.Status(bg); err != nil {
		t.Fatal(err)
	}
	ariaPID := aria2Child(t, cmd.Process.Pid)
	cmd.Process.Signal(syscall.SIGTERM)
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	cmd.Process.Signal(syscall.SIGTERM)
	err := cmd.Wait()
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("second SIGTERM took %s to end the daemon", d)
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); !ok || ee.ExitCode() == 0 {
		t.Errorf("forced exit = %v, want a non-zero exit", err)
	}
	if !waitGone(ariaPID, 3*time.Second) {
		t.Errorf("aria2c %d outlived a forced daemon exit", ariaPID)
	}
}
