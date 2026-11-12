package aria2

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallHint(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("os-release is linux only")
	}
	f := filepath.Join(t.TempDir(), "os-release")
	t.Cleanup(func() { osRelease = "/etc/os-release" })
	osRelease = f
	for body, want := range map[string]string{
		"ID=arch\n": "sudo pacman -S aria2",
		"ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n":               "sudo apt install aria2",
		"ID=\"opensuse-tumbleweed\"\nID_LIKE=\"opensuse suse\"\n": "sudo zypper install aria2",
		"ID=opensuse-leap\n": "sudo zypper install aria2",
		"ID=weird\n":         "install the aria2 package with your package manager",
	} {
		os.WriteFile(f, []byte(body), 0o600)
		if got := InstallHint(); got != want {
			t.Errorf("os-release %q: hint %q, want %q", body, got, want)
		}
	}
}
