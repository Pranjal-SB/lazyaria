package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

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
	if err := checkSocketDir(filepath.Dir(p.Socket)); err != nil {
		return nil, nil, err
	}
	lock, err := takeLock(p.Lock)
	if err != nil {
		return nil, nil, err
	}
	// Holding the lock proves no daemon is serving this socket.
	if err := removeStale(p.Socket); err != nil {
		lock.Close()
		return nil, nil, err
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

// checkSocketDir holds a custom advanced.socket_path dir to the same rule
// as the runtime dir: ours, and closed to everyone else.
func checkSocketDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w; run `lazyaria doctor`", dir, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || !dirIsPrivate(fi) {
		return fmt.Errorf("socket dir %s must be a directory owned by you with mode 0700; fix it with `chmod 700 %s` or pick another advanced.socket_path", dir, dir)
	}
	return nil
}

func removeStale(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode().Type() != os.ModeSocket {
		return fmt.Errorf("%s exists and is not a socket; move it away or pick another advanced.socket_path", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket: %w; run `lazyaria doctor`", err)
	}
	return nil
}
