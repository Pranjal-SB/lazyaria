//go:build linux

package daemon

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func listInhibitors(t *testing.T) string {
	out, err := exec.Command("systemd-inhibit", "--list").CombinedOutput()
	if err != nil {
		t.Skipf("systemd-inhibit unavailable: %v", err)
	}
	return string(out)
}

func TestInhibitHoldRelease(t *testing.T) {
	c, err := dbus.ConnectSystemBus()
	if err != nil {
		t.Skipf("no system bus: %v", err)
	}
	c.Close()
	in := NewInhibitor("block")
	if err := in.Hold("downloads active"); err != nil {
		t.Skipf("logind unavailable: %v", err)
	}
	defer in.Release()
	if !strings.Contains(listInhibitors(t), "lazyaria") {
		t.Fatal("inhibitor not listed after Hold")
	}
	if err := in.Hold("downloads active"); err != nil {
		t.Fatalf("second Hold: %v", err)
	}
	if err := in.Hold("different reason"); err != nil {
		t.Fatalf("Hold new reason: %v", err)
	}
	if !strings.Contains(listInhibitors(t), "different reason") {
		t.Fatal("reason not updated")
	}
	if err := in.Release(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(listInhibitors(t), "lazyaria") {
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
