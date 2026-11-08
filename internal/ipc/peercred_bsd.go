//go:build darwin || freebsd

package ipc

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the effective uid of the process at the other end of c.
func peerUID(c net.Conn) (int, error) {
	return sockopt(c, func(fd int) (int, error) {
		cr, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			return -1, err
		}
		return int(cr.Uid), nil
	})
}
