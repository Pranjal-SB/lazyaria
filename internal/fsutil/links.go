package fsutil

import (
	"errors"
	"syscall"
)

// LinkUnsupported reports whether a link() or symlink() error means the
// filesystem cannot make links. FAT and exFAT answer EPERM.
func LinkUnsupported(err error) bool {
	for _, e := range []syscall.Errno{syscall.EPERM, syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.EXDEV, syscall.EMLINK} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
