// Package buildinfo holds values stamped in at build time.
package buildinfo

// Version is overridden at link time with -ldflags "-X ...buildinfo.Version=...".
var Version = "dev"

// Protocol is the IPC protocol version shared by the CLI and the daemon.
const Protocol = 1
