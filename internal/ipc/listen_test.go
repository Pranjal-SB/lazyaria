package ipc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPerms0600(t *testing.T) {
	_, p := serve(t, newFake())
	fi, err := os.Stat(p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(p.Socket))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("socket dir mode %v, want 0700", di.Mode().Perm())
	}
}

func TestSecondListenerAlreadyRunning(t *testing.T) {
	p := testPaths(shortDir(t))
	l, lock, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	defer lock.Close()
	_, _, err = Listen(p)
	var ar *ErrAlreadyRunning
	if !errors.As(err, &ar) || ar.PID != os.Getpid() {
		t.Fatalf("second Listen: %v, want ErrAlreadyRunning{PID: %d}", err, os.Getpid())
	}
	if _, err := os.Stat(p.Socket); err != nil {
		t.Errorf("the losing Listen removed the live socket: %v", err)
	}
}

func TestStaleSocketRemoved(t *testing.T) {
	p := testPaths(shortDir(t))
	if err := os.MkdirAll(p.RuntimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old, err := net.Listen("unix", p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	old.Close() // leaves the socket file behind, like a crashed daemon
	l, lock, err := Listen(p)
	if err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	l.Close()
	lock.Close()
}

func TestListenRefusesNonSocket(t *testing.T) {
	p := testPaths(shortDir(t))
	if err := os.MkdirAll(p.RuntimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Socket, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Listen(p); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("Listen over a regular file: %v", err)
	}
	if b, _ := os.ReadFile(p.Socket); string(b) != "keep" {
		t.Fatal("Listen removed a file that was not a socket")
	}
}

func TestListenRefusesOpenSocketDir(t *testing.T) {
	d := shortDir(t)
	p := testPaths(d)
	shared := filepath.Join(d, "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(shared, 0o755)
	p.Socket = filepath.Join(shared, "s.sock")
	if _, _, err := Listen(p); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("Listen in a 0755 dir: %v", err)
	}
}
