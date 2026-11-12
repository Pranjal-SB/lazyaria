package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// stubSystemctl puts a systemctl on PATH that logs its arguments.
func stubSystemctl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\ncase \"$*\" in *is-active*) exit ${STUB_ACTIVE_EXIT:-0};; esac\n"
	os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o700)
	old := settleDelay
	settleDelay = 10 * time.Millisecond
	t.Cleanup(func() { settleDelay = old })
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	return log
}

func TestServiceInstallWritesUnit(t *testing.T) {
	isolate(t)
	calls := stubSystemctl(t)
	code, out, errOut := run(t, Service, "", "install")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	exe, _ := os.Executable()
	b, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "systemd", "user", "lazyaria.service"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(b)
	for _, want := range []string{"ExecStart=" + exe + " daemon --mode=always", "Restart=on-failure", "KillMode=mixed", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q:\n%s", want, unit)
		}
	}
	c, _ := os.ReadFile(calls)
	if !strings.Contains(string(c), "--user daemon-reload\n--user enable lazyaria\n--user restart lazyaria\n--user is-active --quiet lazyaria") {
		t.Errorf("systemctl calls:\n%s", c)
	}

	if code, _, errOut := run(t, Service, "", "uninstall"); code != 0 {
		t.Fatalf("uninstall exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "systemd", "user", "lazyaria.service")); !os.IsNotExist(err) {
		t.Errorf("unit still there: %v", err)
	}
	c, _ = os.ReadFile(calls)
	if !strings.Contains(string(c), "--user disable --now lazyaria") {
		t.Errorf("systemctl calls:\n%s", c)
	}
}

func TestServiceWithoutSystemd(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", t.TempDir())
	code, out, _ := run(t, Service, "", "install")
	if code != 1 || !strings.Contains(out, "daemon --mode=always") {
		t.Fatalf("exit %d: %q", code, out)
	}
}

func TestServiceInstallStopsRunningDaemon(t *testing.T) {
	f := startFake(t)
	calls := stubSystemctl(t)
	stand := exec.Command("sleep", "60") // stands in for the daemon process
	if err := stand.Start(); err != nil {
		t.Fatal(err)
	}
	f.pid = stand.Process.Pid
	go func() { stand.Wait(); f.stop() }()
	t.Cleanup(func() { stand.Process.Kill() })

	code, out, errOut := run(t, Service, "", "install")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if !strings.Contains(out, "continue under the service") {
		t.Errorf("stdout %q", out)
	}
	if !waitExited(stand, 5*time.Second) {
		t.Error("the running daemon was not stopped")
	}
	if c, _ := os.ReadFile(calls); !strings.Contains(string(c), "--user restart lazyaria") {
		t.Errorf("systemctl calls:\n%s", c)
	}
}

func TestServiceInstallNotActive(t *testing.T) {
	isolate(t)
	stubSystemctl(t)
	t.Setenv("STUB_ACTIVE_EXIT", "3")
	code, _, errOut := run(t, Service, "", "install")
	if code != 1 || !strings.Contains(errOut, "journalctl --user -u lazyaria") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestUnitEscapesPercent(t *testing.T) {
	if got := unitExec("/opt/50%/lazyaria"); got != "/opt/50%%/lazyaria" {
		t.Errorf("unitExec = %q", got)
	}
	if got := unitExec("/opt/my apps/lazyaria"); got != `"/opt/my apps/lazyaria"` {
		t.Errorf("unitExec = %q", got)
	}
}

func waitExited(c *exec.Cmd, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if c.Process.Signal(syscall.Signal(0)) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
