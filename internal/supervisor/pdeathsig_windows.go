package supervisor

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }
