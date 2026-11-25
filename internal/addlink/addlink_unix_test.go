//go:build unix

package addlink

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Only a plain file of sane size is read: a FIFO would block for ever and
// a device would never end.
func TestParamsRefusesOddFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.torrent")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	zero := filepath.Join(dir, "zero.torrent")
	if err := os.Symlink("/dev/zero", zero); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.torrent")
	if err := os.WriteFile(big, make([]byte, MaxTorrent+1), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{fifo, zero, big} {
		done := make(chan error, 1)
		go func() { _, err := Params(p); done <- err }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("Params(%s) read it", p)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("Params(%s) hangs", p)
		}
	}
}
