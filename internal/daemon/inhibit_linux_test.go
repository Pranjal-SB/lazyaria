//go:build linux

package daemon

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// hasInhibitor reports whether logind lists a lazyaria inhibitor with this
// exact reason taken by this process. Matching the reason and pid keeps a
// running daemon or another test run on the same machine from counting.
func hasInhibitor(t *testing.T, c *dbus.Conn, why string) bool {
	t.Helper()
	var list []struct {
		What, Who, Why, Mode string
		UID, PID             uint32
	}
	err := c.Object("org.freedesktop.login1", "/org/freedesktop/login1").
		Call("org.freedesktop.login1.Manager.ListInhibitors", 0).Store(&list)
	if err != nil {
		t.Skipf("logind unavailable, cannot list inhibitors: %v", err)
	}
	for _, i := range list {
		if i.Who == "lazyaria" && i.Why == why && i.PID == uint32(os.Getpid()) {
			return true
		}
	}
	return false
}

// waitInhibitor polls until the inhibitor's presence matches want. logind
// drops a lock only after it sees the file descriptor close, so a single
// check right after Release can race it.
func waitInhibitor(t *testing.T, c *dbus.Conn, why string, want bool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if hasInhibitor(t, c, why) == want {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestInhibitHoldRelease(t *testing.T) {
	c, err := dbus.ConnectSystemBus()
	if err != nil {
		t.Skipf("no system bus, logind unavailable: %v", err)
	}
	defer c.Close()
	tag := fmt.Sprintf("lazyaria test %d-%d", os.Getpid(), time.Now().UnixNano())
	first, second := tag+" first", tag+" second"

	in := NewInhibitor("block")
	if err := in.Hold(first); err != nil {
		t.Skipf("logind unavailable or refused the inhibitor: %v", err)
	}
	defer in.Release()
	if !waitInhibitor(t, c, first, true) {
		t.Fatal("inhibitor not listed after Hold")
	}
	if err := in.Hold(first); err != nil {
		t.Fatalf("second Hold: %v", err)
	}
	if err := in.Hold(second); err != nil {
		t.Fatalf("Hold new reason: %v", err)
	}
	if !waitInhibitor(t, c, second, true) {
		t.Fatal("reason not updated")
	}
	if !waitInhibitor(t, c, first, false) {
		t.Fatal("old inhibitor still listed after reason change")
	}
	if err := in.Release(); err != nil {
		t.Fatal(err)
	}
	if !waitInhibitor(t, c, second, false) {
		t.Fatal("inhibitor still listed after Release")
	}
	if err := in.Release(); err != nil {
		t.Fatalf("double Release: %v", err)
	}
}

func TestInhibitNoBusIsError(t *testing.T) {
	in := &logindInhibitor{mode: "block", dial: func() (*dbus.Conn, error) { return nil, errors.New("no bus") }}
	if err := in.Hold("x"); err == nil {
		t.Fatal("expected error")
	}
	if err := in.Release(); err != nil {
		t.Fatal(err)
	}
}
