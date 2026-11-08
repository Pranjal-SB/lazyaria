//go:build freebsd

package supervisor

import "syscall"

// FreeBSD's parent-death signal is per process, so it is safe here. Setpgid
// keeps a terminal Ctrl-C away from aria2c.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
}
