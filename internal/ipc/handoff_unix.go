//go:build !windows

package ipc

import (
	"cmp"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// Handoff readies l and lock for an exec of the daemon binary. It returns
// a close-on-exec duplicate of the listening socket (the caller owns it)
// and the lock's fd; KeepOnExec makes both inheritable just before the
// exec. Closing l no longer removes the socket file.
func Handoff(l net.Listener, lock *Lock) (listenFD, lockFD int, err error) {
	ul, ok := l.(*net.UnixListener)
	if !ok {
		return 0, 0, fmt.Errorf("cannot hand over a %T", l)
	}
	// Control rather than File: File's Fd would switch the socket, which
	// Serve still uses, to blocking mode.
	var derr error
	rc, err := ul.SyscallConn()
	if err == nil {
		err = rc.Control(func(fd uintptr) { listenFD, derr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0) })
	}
	if err = cmp.Or(err, derr); err != nil {
		return 0, 0, fmt.Errorf("duplicate the socket: %w", err)
	}
	if rc, err = lock.f.SyscallConn(); err == nil {
		err = rc.Control(func(fd uintptr) { lockFD = int(fd) })
	}
	if err != nil {
		unix.Close(listenFD)
		return 0, 0, err
	}
	ul.SetUnlinkOnClose(false)
	return listenFD, lockFD, nil
}

// KeepOnExec clears close-on-exec on fds so an exec'd image inherits them.
func KeepOnExec(fds ...int) error {
	for _, fd := range fds {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
			return fmt.Errorf("keep fd %d across exec: %w", fd, err)
		}
	}
	return nil
}

// Adopt takes over the listener and lock that a previous image of this
// daemon handed over. Neither is re-created: the lock stays held and
// clients queued on the socket are kept. Closing the adopted listener does
// not remove the socket file.
func Adopt(listenFD, lockFD int) (net.Listener, *Lock, error) {
	unix.CloseOnExec(lockFD)
	f := os.NewFile(uintptr(listenFD), "lazyaria.sock")
	defer f.Close()
	l, err := net.FileListener(f) // dups with close-on-exec
	if err != nil {
		return nil, nil, fmt.Errorf("adopt the socket from the previous daemon: %w; run `lazyaria doctor`", err)
	}
	return l, &Lock{f: os.NewFile(uintptr(lockFD), "daemon.lock")}, nil
}
