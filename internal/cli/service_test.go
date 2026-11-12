package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSystemctl puts a systemctl on PATH that logs its arguments.
func stubSystemctl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\necho \"$@\" >> "+log+"\n"), 0o700)
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
	if !strings.Contains(string(c), "--user daemon-reload") || !strings.Contains(string(c), "--user enable --now lazyaria") {
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
