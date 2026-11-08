//go:build !linux && !freebsd

package supervisor

import "syscall"

// No parent-death signal here; aria2c's --stop-with-process covers it.
func sysProcAttr() *syscall.SysProcAttr { return nil }
