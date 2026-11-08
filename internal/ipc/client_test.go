package ipc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHelloUpgrade(t *testing.T) {
	f := newFake()
	f.version = "0.0.1"
	c, _ := serve(t, f)
	res, upgraded, err := c.helloAs(context.Background(), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !upgraded || res.DaemonVersion != "0.0.1" {
		t.Fatalf("upgraded = %v, res = %+v", upgraded, res)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upgrades != 1 {
		t.Fatalf("daemon.upgrade called %d times, want 1", f.upgrades)
	}
}

func TestHelloSameVersionNoUpgrade(t *testing.T) {
	f := newFake()
	f.version = "0.1.0"
	c, _ := serve(t, f)
	if _, upgraded, err := c.helloAs(context.Background(), "0.1.0"); err != nil || upgraded {
		t.Fatalf("upgraded = %v, err = %v", upgraded, err)
	}
}

func TestHelloProtocolMismatch(t *testing.T) {
	f := newFake()
	f.version, f.protocol = "0.0.1", 99
	c, _ := serve(t, f)
	_, _, err := c.Hello(context.Background())
	var pm *ErrProtocolMismatch
	if !errors.As(err, &pm) || !strings.Contains(err.Error(), "it will upgrade when idle") {
		t.Fatalf("err = %v, want ErrProtocolMismatch banner", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upgrades != 0 {
		t.Fatal("daemon.upgrade called despite a protocol mismatch")
	}
}

func TestOlder(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"0.0.1", "0.1.0", true},
		{"v0.1.0", "0.1.1", true},
		{"0.1.10", "0.1.9", false},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "dev", true},
		{"dev", "0.9.0", false},
		{"dev", "dev", false},
		{"1.0.0-rc1", "1.0.0", false}, // pre-release tags are ignored
		{"garbage", "0.1.0", false},
	} {
		if got := older(tc.a, tc.b); got != tc.want {
			t.Errorf("older(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
