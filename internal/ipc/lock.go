package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/Pranjal-SB/lazyaria/internal/config"
)

// ErrAlreadyRunning means another daemon holds the lock.
type ErrAlreadyRunning struct{ PID int }

func (e *ErrAlreadyRunning) Error() string {
	return fmt.Sprintf("a lazyaria daemon is already running (pid %d); use it, or stop it before starting another", e.PID)
}

// Lock is the daemon's exclusive lock; it is released on Close or exit.
type Lock struct{ f *os.File }

func (l *Lock) Close() error { return l.f.Close() }

// Listen takes the daemon lock, clears a socket left by a dead daemon and
// listens on p.Socket with mode 0600 inside a 0700 dir.
func Listen(p config.Paths) (net.Listener, *Lock, error) {
	if err := config.EnsureDirs(p); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p.Socket), 0o700); err != nil {
		return nil, nil, err
	}
	lock, err := takeLock(p.Lock)
	if err != nil {
		return nil, nil, err
	}
	// Holding the lock proves no daemon is serving this socket.
	if err := os.Remove(p.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		lock.Close()
		return nil, nil, fmt.Errorf("remove stale socket: %w; run `lazyaria doctor`", err)
	}
	l, err := net.Listen("unix", p.Socket)
	if err != nil {
		lock.Close()
		return nil, nil, fmt.Errorf("listen on %s: %w; run `lazyaria doctor`", p.Socket, err)
	}
	if err := os.Chmod(p.Socket, 0o600); err != nil {
		l.Close()
		lock.Close()
		return nil, nil, fmt.Errorf("chmod socket: %w; run `lazyaria doctor`", err)
	}
	return l, lock, nil
}

func takeLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w; run `lazyaria doctor`", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			b, _ := os.ReadFile(path)
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			return nil, &ErrAlreadyRunning{PID: pid}
		}
		return nil, fmt.Errorf("lock %s: %w; run `lazyaria doctor`", path, err)
	}
	if err := f.Truncate(0); err == nil {
		f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &Lock{f: f}, nil
}
