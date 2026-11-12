//go:build e2e && linux

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// testDiskFull downloads 2 MiB into a 1 MiB tmpfs. The tmpfs lives in a
// private user+mount namespace, so the daemon runs inside it; the test
// talks to it through the socket, which sits outside the tmpfs.
func testDiskFull(t *testing.T) {
	t.Parallel()
	if out, err := exec.Command("unshare", "-rm", "sh", "-c", "mount -t tmpfs -o size=1m tmpfs \"$1\"", "sh", t.TempDir()).CombinedOutput(); err != nil {
		t.Skipf("cannot mount a tmpfs in a user namespace (unshare -rm): %v %s", err, out)
	}
	srv := testutil.NewFaultServer(t, map[string][]byte{"big.bin": payload(2<<20, 0x51)})
	// Without the precheck turned off the job would be paused for lack of
	// space before aria2 ever hits the full disk.
	e := newEnv(t, "[reliability]\ndisk_precheck = false\n")
	d := exec.Command("unshare", "-rm", "sh", "-c", "mount -t tmpfs -o size=1m tmpfs \"$1\" && exec \"$2\" daemon", "sh", e.dl, bin)
	d.Env = e.vars
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Process.Signal(syscall.SIGTERM)
		d.Wait()
	})

	c := e.dial()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	events, _, err := c.Subscribe(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	j, err := c.Add(ctx, daemon.AddParams{URIs: []string{srv.URL("big.bin", testutil.ModeOK)}})
	if err != nil {
		t.Fatal(err)
	}
	var banner map[string]any
	for banner == nil {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("lost the daemon while waiting for the disk-full banner")
			}
			if ev.Type == "banner" && ev.Data["kind"] == "disk_full" {
				banner = ev.Data
			}
		case <-ctx.Done():
			t.Fatalf("no disk_full banner; job is %+v", e.job(j.ID))
		}
	}
	if banner["dir"] != e.dl {
		t.Errorf("banner dir %v, want %s", banner["dir"], e.dl)
	}
	got := e.job(j.ID)
	t.Logf("banner %v; job %s/%s: %s", banner, got.State, deref(got.ErrorKind), deref(got.ErrorMsg))
	if got.State != store.Error || deref(got.ErrorKind) != "disk-full" {
		t.Errorf("job is %s/%s (%s), want error/disk-full", got.State, deref(got.ErrorKind), deref(got.ErrorMsg))
	}
}

// testCrashLoop points engine.aria2c_path at a binary that passes the
// version check and then always exits 1: the daemon stays up and reports
// engine.state=failed instead of respawning forever.
func testCrashLoop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fake := filepath.Join(dir, "aria2c")
	script := "#!/bin/sh\n[ \"$1\" = --version ] && { echo 'aria2 version 1.37.0'; exit 0; }\necho 'simulated crash' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, "[engine]\naria2c_path = \""+fake+"\"\nrestart_max_backoff = \"200ms\"\n")
	waitFor(t, 30*time.Second, "engine_state failed", func() bool {
		return e.status().EngineState == "failed"
	})
	if pid := e.status().PID; !alive(pid) {
		t.Errorf("daemon %d died in the crash loop", pid)
	}
}
