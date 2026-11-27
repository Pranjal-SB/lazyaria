// Package buildinfo holds values stamped in at build time.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version is overridden at link time with -ldflags "-X ...buildinfo.Version=...".
var Version = "dev"

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
