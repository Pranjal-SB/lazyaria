package ipc

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// Handoff prepares l and lock to survive an exec of the daemon binary: the
// returned listener file and lock fd are inheritable, and closing l no
// longer removes the socket file. The caller keeps listen open until the
// exec and passes both fds to the new image, which calls Adopt.
func Handoff(l net.Listener, lock *Lock) (listen *os.File, lockFD int, err error) {
	ul, ok := l.(*net.UnixListener)
	if !ok {
		return nil, 0, fmt.Errorf("cannot hand over a %T", l)
	}
	listen, err = ul.File() // a dup; the listener itself is closed by Serve
	if err != nil {
		return nil, 0, err
	}
	ul.SetUnlinkOnClose(false)
	for _, fd := range []uintptr{listen.Fd(), lock.f.Fd()} {
		if _, err := unix.FcntlInt(fd, unix.F_SETFD, 0); err != nil {
			listen.Close()
			return nil, 0, fmt.Errorf("keep fd %d across exec: %w", fd, err)
		}
	}
	return listen, int(lock.f.Fd()), nil
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
