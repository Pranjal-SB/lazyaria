package aria2

import (
	"os"
	"runtime"
	"strings"
)

// installCmds maps an os-release ID (or ID_LIKE entry) to its install command.
var installCmds = map[string]string{
	"arch":     "sudo pacman -S aria2",
	"debian":   "sudo apt install aria2",
	"ubuntu":   "sudo apt install aria2",
	"fedora":   "sudo dnf install aria2",
	"rhel":     "sudo dnf install aria2",
	"opensuse": "sudo zypper install aria2",
	"suse":     "sudo zypper install aria2",
	"alpine":   "sudo apk add aria2",
	"gentoo":   "sudo emerge net-misc/aria2",
	"void":     "sudo xbps-install aria2",
	"nixos":    "nix-env -iA nixpkgs.aria2",
}

var osRelease = "/etc/os-release"

// InstallHint is the command that installs aria2 on this system, as far as
// /etc/os-release tells.
func InstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install aria2"
	case "freebsd":
		return "sudo pkg install aria2"
	}
	b, _ := os.ReadFile(osRelease)
	var ids []string
	for _, l := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(l, "=")
		if k == "ID" || k == "ID_LIKE" {
			ids = append(ids, strings.Fields(strings.Trim(v, `"'`))...)
		}
	}
	for _, id := range ids {
		if c, ok := installCmds[id]; ok {
			return c
		}
	}
	return "install the aria2 package with your package manager"
}
