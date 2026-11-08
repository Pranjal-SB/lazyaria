//go:build linux

package supervisor

import "syscall"

// No Pdeathsig: on Linux it fires when the spawning thread exits, not the
// process, and it would kill aria2c across the exec upgrade. aria2's
// --stop-with-process covers parent death. Setpgid keeps a terminal Ctrl-C
// away from aria2c.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
