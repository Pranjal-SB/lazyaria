//go:build linux

package daemon

import (
	"fmt"
	"sync"
	"syscall"

	"github.com/godbus/dbus/v5"
)

// logindInhibitor takes a logind sleep inhibitor lock. The bus is dialled
// lazily on Hold, so a machine without logind never stops the daemon from
// starting; Hold just returns an error the caller can log.
type logindInhibitor struct {
	mode   string
	dial   func() (*dbus.Conn, error)
	mu     sync.Mutex
	conn   *dbus.Conn
	fd     int
	held   bool
	reason string
}

// NewInhibitor returns an Inhibitor for mode "block" or "delay".
func NewInhibitor(mode string) Inhibitor {
	if mode != "delay" {
		mode = "block"
	}
	return &logindInhibitor{mode: mode, dial: func() (*dbus.Conn, error) { return dbus.ConnectSystemBus() }}
}

func (l *logindInhibitor) Hold(reason string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held && l.reason == reason {
		return nil
	}
	if l.conn == nil {
		c, err := l.dial()
		if err != nil {
			return fmt.Errorf("sleep inhibit unavailable, no system bus (downloads still run, machine may sleep): %w", err)
		}
		l.conn = c
	}
	var fd dbus.UnixFD
	err := l.conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").
		Call("org.freedesktop.login1.Manager.Inhibit", 0, "sleep", "lazyaria", reason, l.mode).Store(&fd)
	if err != nil {
		return fmt.Errorf("logind refused sleep inhibit (downloads still run, machine may sleep): %w", err)
	}
	// The new lock is held; only now drop the old one.
	old, hadOld := l.fd, l.held
	l.fd, l.held, l.reason = int(fd), true, reason
	if hadOld {
		syscall.Close(old)
	}
	return nil
}

func (l *logindInhibitor) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	if l.held {
		err = syscall.Close(l.fd)
		l.held, l.reason = false, ""
	}
	if l.conn != nil {
		l.conn.Close()
		l.conn = nil
	}
	return err
}
