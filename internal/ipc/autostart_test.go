package ipc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
)

func TestAutoStart_RaceTwoClients(t *testing.T) {
	dir := shortDir(t)
	p := testPaths(dir)
	t.Setenv("LAZYARIA_TEST_FAKE_DAEMON", "1")
	t.Setenv("LAZYARIA_TEST_DIR", dir)
	t.Cleanup(func() { stopDaemon(t, p) })

	var wg sync.WaitGroup
	clients := make([]*Client, 2)
	errs := make([]error, 2)
	for i := range clients {
		wg.Go(func() { clients[i], errs[i] = Connect(context.Background(), p, true, os.Args[0]) })
	}
	wg.Wait()

	pids := map[int]bool{}
	for i, c := range clients {
		if errs[i] != nil {
			t.Fatalf("client %d: %v", i, errs[i])
		}
		defer c.Close()
		st, err := c.Status(context.Background())
		if err != nil {
			t.Fatalf("client %d status: %v", i, err)
		}
		pids[st.PID] = true
	}
	if len(pids) != 1 {
		t.Fatalf("clients reached different daemons: %v", pids)
	}
	if pid := lockPID(t, p); !pids[pid] {
		t.Fatalf("lock held by %d, clients talk to %v", pid, pids)
	}

	// Both racers spawn a daemon; the loser must back off on the lock.
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(dir, "log"))
		log := string(b)
		if strings.Count(log, "busy") == 1 || time.Now().After(deadline) {
			if n := strings.Count(log, "listening"); n != 1 {
				t.Fatalf("%d daemons listened, want 1; log:\n%s", n, log)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestConnectUnreachable(t *testing.T) {
	p := testPaths(shortDir(t))
	_, err := Connect(context.Background(), p, false, "")
	if !errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), "lazyaria doctor") {
		t.Fatalf("err = %v, want ErrUnreachable pointing to doctor", err)
	}
}

func TestConnectFailsFastWhenDaemonDies(t *testing.T) {
	dir := shortDir(t)
	t.Setenv("LAZYARIA_TEST_FAKE_DAEMON", "fail")
	start := time.Now()
	_, err := Connect(context.Background(), testPaths(dir), true, os.Args[0])
	if !errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), "sudo pacman -S aria2") {
		t.Fatalf("err = %v, want ErrUnreachable with the daemon's stderr", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v; should fail as soon as the daemon exits", d)
	}
}

func lockPID(t *testing.T, p config.Paths) int {
	t.Helper()
	b, err := os.ReadFile(p.Lock)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// stopDaemon terminates the daemon a test auto-started and waits for it
// to let go of its socket.
func stopDaemon(t *testing.T, p config.Paths) {
	pid := lockPID(t, p)
	if pid <= 0 {
		return
	}
	syscall.Kill(pid, syscall.SIGTERM)
	for range 150 {
		if _, err := os.Stat(p.Socket); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
}

// A daemon that is idle-exiting has already unlinked its socket but still
// holds the lock while aria2 shuts down. The daemon we spawn then backs off
// and exits 0; Connect must spawn again instead of waiting out the budget.
func TestConnectRespawnsWhileOldDaemonExits(t *testing.T) {
	dir := shortDir(t)
	p := testPaths(dir)
	t.Setenv("LAZYARIA_TEST_FAKE_DAEMON", "1")
	t.Setenv("LAZYARIA_TEST_DIR", dir)
	if err := os.MkdirAll(p.RuntimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old, err := takeLock(p.Lock)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(500*time.Millisecond, func() { old.Close() })
	t.Cleanup(func() {
		if pid := lockPID(t, p); pid != os.Getpid() {
			stopDaemon(t, p)
		}
	})

	c, err := Connect(context.Background(), p, true, os.Args[0])
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
}
