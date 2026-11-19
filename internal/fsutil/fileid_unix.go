//go:build unix

package fsutil

import (
	"fmt"
	"os"
	"syscall"
)

// FileID returns the inode, device and size of path, not following a final
// symlink. Directories report size 0.
func FileID(path string) (ino, dev uint64, size int64, err error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, 0, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, fmt.Errorf("%s: no inode number on this system", path)
	}
	if !fi.IsDir() {
		size = fi.Size()
	}
	return uint64(st.Ino), uint64(st.Dev), size, nil //nolint:unconvert // widths differ per OS
}
