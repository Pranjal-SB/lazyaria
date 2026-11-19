//go:build unix

package fsutil

import (
	"fmt"
	"os"
	"syscall"
)

// FileID returns the inode and size of path, not following a final symlink.
// Directories report size 0.
func FileID(path string) (ino uint64, size int64, err error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("%s: no inode number on this system", path)
	}
	if !fi.IsDir() {
		size = fi.Size()
	}
	return uint64(st.Ino), size, nil //nolint:unconvert // Ino width differs per OS
}
