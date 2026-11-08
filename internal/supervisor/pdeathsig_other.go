//go:build !linux && !freebsd

package supervisor

import "syscall"

// No parent-death signal here; aria2c's --stop-with-process covers it.
// Setpgid keeps a terminal Ctrl-C away from aria2c.
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
