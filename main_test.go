package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"lazyaria", "version"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, errb.String())
	}
	if got, want := out.String(), "lazyaria dev (protocol 1)\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunUnknown(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"lazyaria", "nope"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	msg := errb.String()
	if !strings.Contains(msg, `unknown command "nope"`) {
		t.Errorf("stderr %q missing unknown command message", msg)
	}
	if !strings.Contains(msg, "lazyaria help") {
		t.Errorf("stderr %q missing fix hint", msg)
	}
}
