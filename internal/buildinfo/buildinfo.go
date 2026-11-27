// Package buildinfo holds values stamped in at build time.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version is overridden at link time with -ldflags "-X ...buildinfo.Version=...".
var Version = "dev"

// Source says how the binary was shipped, set at link time like Version:
// "release" for the GitHub release tarballs (which the Homebrew cask and the
// AUR lazyaria-bin package reuse). A package that builds from source can set
// its own name, such as "homebrew" or "aur". Empty for a build from source.
var Source string

// Protocol is the IPC protocol version shared by the CLI and the daemon.
const Protocol = 1

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		Version = moduleVersion(Version, info)
	}
}

// moduleVersion keeps a version set by ldflags. Otherwise it takes the module
// version that "go install ...@v1.2.3" records. A build from a local checkout
// carries vcs settings and stays "dev", so it still counts as newer than any
// release when the CLI and the daemon compare versions.
func moduleVersion(v string, info *debug.BuildInfo) string {
	mv := info.Main.Version
	if v != "dev" || mv == "" || mv == "(devel)" {
		return v
	}
	for _, s := range info.Settings {
		if strings.HasPrefix(s.Key, "vcs.") {
			return v
		}
	}
	return strings.TrimPrefix(mv, "v")
}
