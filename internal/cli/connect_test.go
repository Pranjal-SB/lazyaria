package cli

import (
	"testing"
)

// The daemon's binary is no newer than the daemon, so "upgrading" brings
// back the same version. The client must ask once and then use it.
func TestConnectAsksUpgradeOnce(t *testing.T) {
	f := startFake(t)
	f.version = "0.0.1"
	code, _, errOut := run(t, List, "")
	if code != exitOK {
		t.Fatalf("list exited %d: %s", code, errOut)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upgrades != 1 {
		t.Fatalf("daemon.upgrade asked %d times, want 1", f.upgrades)
	}
}

// A request cut off by the daemon re-executing itself is sent again, once,
// to the same daemon process.
func TestRetryAfterUpgradeHangup(t *testing.T) {
	f := startFake(t)
	f.hangups = 1
	code, _, errOut := run(t, List, "")
	if code != exitOK {
		t.Fatalf("list exited %d: %s", code, errOut)
	}
	f.mu.Lock()
	f.hangups = 2
	f.mu.Unlock()
	if code, _, errOut = run(t, List, ""); code == exitOK {
		t.Fatalf("list succeeded after two hang-ups; want one retry only (%s)", errOut)
	}
}

// When another process holds the lock after the hang-up, the daemon was
// replaced rather than upgraded and the request may have run: no retry.
func TestNoRetryWhenDaemonReplaced(t *testing.T) {
	f := startFake(t)
	f.hangups, f.newPID = 1, 1
	if code, _, _ := run(t, List, ""); code == exitOK {
		t.Fatal("list was retried on a different daemon")
	}
}
