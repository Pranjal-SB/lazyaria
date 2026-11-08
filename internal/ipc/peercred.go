package ipc

import (
	"fmt"
	"net"
)

// sockopt runs get on c's file descriptor.
func sockopt(c net.Conn, get func(fd int) (int, error)) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, fmt.Errorf("not a unix socket: %T", c)
	}
	rc, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	uid, gerr := -1, error(nil)
	if err := rc.Control(func(fd uintptr) { uid, gerr = get(int(fd)) }); err != nil {
		return -1, err
	}
	return uid, gerr
}
