package aria2

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeAria(t *testing.T, line string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "aria2c")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+line+"'\necho 'second line'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckVersion_TooOld(t *testing.T) {
	_, err := CheckVersion(fakeAria(t, "aria2 version 1.34.0"))
	if !errors.Is(err, ErrTooOld) || !strings.Contains(err.Error(), "aria2c 1.34.0 is too old; need ≥ 1.35.0") {
		t.Fatalf("err %v", err)
	}
}

func TestCheckVersion_OK(t *testing.T) {
	v, err := CheckVersion(fakeAria(t, "aria2 version 1.37.0"))
	if err != nil || v != "1.37.0" {
		t.Fatalf("%q %v", v, err)
	}
}

func TestCheckVersion_Missing(t *testing.T) {
	_, err := CheckVersion(filepath.Join(t.TempDir(), "nope"))
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "aria2c not found") {
		t.Fatalf("err %v", err)
	}
}

func TestCheckVersion_Garbage(t *testing.T) {
	if _, err := CheckVersion(fakeAria(t, "hello")); err == nil {
		t.Fatal("want error")
	}
}

func script(t *testing.T, mode os.FileMode, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "aria2c")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckVersion_Timeout(t *testing.T) {
	old := versionTimeout
	versionTimeout = 200 * time.Millisecond
	defer func() { versionTimeout = old }()
	start := time.Now()
	_, err := CheckVersion(script(t, 0o755, "exec sleep 30\n"))
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "lazyaria doctor") {
		t.Fatalf("err %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("did not time out promptly")
	}
}

func TestCheckVersion_NotExecutable(t *testing.T) {
	_, err := CheckVersion(script(t, 0o644, "echo hi\n"))
	if !errors.Is(err, ErrNotExecutable) || errors.Is(err, ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestCheckVersion_Banner(t *testing.T) {
	v, err := CheckVersion(script(t, 0o755, "echo 'warning: something'\necho 'aria2 version 1.36.0'\n"))
	if err != nil || v != "1.36.0" {
		t.Fatalf("%q %v", v, err)
	}
}
