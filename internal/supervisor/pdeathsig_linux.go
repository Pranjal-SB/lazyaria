//go:build linux

package supervisor

import "syscall"

// The kernel sends SIGTERM to aria2c as soon as we die, even on SIGKILL.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
