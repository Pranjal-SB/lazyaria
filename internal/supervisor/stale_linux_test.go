//go:build linux

package supervisor

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
)

// startFake starts a process whose arguments look like an aria2c that this
// instance launched with --stop-with-process=owner.
func startFake(t *testing.T, conf string, owner int) *exec.Cmd {
	t.Helper()
	// The trailing ":" keeps sh from exec'ing sleep, so the arguments stay.
	cmd := exec.Command("sh", "-c", "sleep 30; :", "aria2c", "--conf-path="+conf, "--stop-with-process="+strconv.Itoa(owner))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd
}

// A daemon killed with SIGKILL leaves its aria2c running until aria2c's
// next parent check; the next daemon stops it before starting its own.
func TestStopStale(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "aria2.conf")
	sv := &Supervisor{p: config.Paths{AriaConf: conf}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	stale := startFake(t, conf, 1)
	ours := startFake(t, conf, os.Getpid())
	other := startFake(t, conf+".other", 1)
	time.Sleep(100 * time.Millisecond) // let sh start
	sv.stopStale()
	if !gone(stale.Process.Pid) {
		t.Error("the previous daemon's aria2c is still running")
	}
	if gone(ours.Process.Pid) {
		t.Error("stopped an aria2c this daemon runs")
	}
	if gone(other.Process.Pid) {
		t.Error("stopped another instance's aria2c")
	}
}
