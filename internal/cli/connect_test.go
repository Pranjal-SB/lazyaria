package cli

import (
	"strings"
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

// An add cut off by the daemon re-executing itself may already have run, so
// it is not sent again; the user is told it may or may not have happened.
func TestNoRetryForAddAfterHangup(t *testing.T) {
	f := startFake(t)
	f.hangups = 1
	code, _, errOut := run(t, Add, "", "https://a/b")
	if code != exitRuntime || !strings.Contains(errOut, "restarted") || !strings.Contains(errOut, "may or may not have taken effect") {
		t.Fatalf("exit %d, stderr %q; want 1 saying the daemon restarted", code, errOut)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.adds) != 1 {
		t.Fatalf("jobs.add sent %d times, want 1", len(f.adds))
	}
}

// A cut-off add is not resent, but the links after it still go out on the
// new connection.
func TestAddAfterCutOffSendsTheRest(t *testing.T) {
	f := startFake(t)
	f.hangups = 1
	code, out, errOut := run(t, Add, "", "https://a/1", "https://a/2", "https://a/3")
	f.mu.Lock()
	defer f.mu.Unlock()
	if code != exitRuntime || len(f.adds) != 3 || strings.Count(errOut, "may or may not") != 1 || strings.Count(out, "added ") != 2 {
		t.Fatalf("exit %d, %d adds sent, stdout %q, stderr %q; want 3 adds, 2 added and one cut-off error", code, len(f.adds), out, errOut)
	}
}

// With --wait the client is subscribed and cannot reconnect, so it stops
// at the cut-off and names the links it never sent.
func TestAddWaitStopsAtCutOff(t *testing.T) {
	f := startFake(t)
	f.hangups = 1
	code, _, errOut := run(t, Add, "", "--wait", "https://a/1", "https://a/2", "https://a/3")
	f.mu.Lock()
	defer f.mu.Unlock()
	if code != exitRuntime || len(f.adds) != 1 || strings.Count(errOut, "may or may not") != 1 ||
		!strings.Contains(errOut, "https://a/2") || !strings.Contains(errOut, "https://a/3") {
		t.Fatalf("exit %d, %d adds sent, stderr %q; want 1 add and the unsent links named", code, len(f.adds), errOut)
	}
}
