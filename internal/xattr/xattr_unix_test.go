//go:build linux || darwin

package xattr

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestStampOrigin(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := StampOrigin(p, "https://u:p@example.com/x", "https://ref.example/"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 256)
	n, err := unix.Getxattr(p, "user.xdg.origin.url", buf)
	if err == unix.ENOTSUP {
		t.Skip("filesystem has no xattr support")
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "https://example.com/x" {
		t.Fatalf("origin = %q", got)
	}
	n, err = unix.Getxattr(p, "user.xdg.referrer.url", buf)
	if err != nil || string(buf[:n]) != "https://ref.example/" {
		t.Fatalf("referrer = %q err %v", buf[:n], err)
	}
}

func TestSupported(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "probe")
	os.WriteFile(p, nil, 0o600)
	want := unix.Setxattr(p, "user.x", []byte("1"), 0) == nil
	if got := Supported(dir); got != want {
		t.Fatalf("Supported = %v, want %v", got, want)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Errorf("probe file left behind: %v", ents)
	}
}
