package fsutil

import (
	"errors"

	"golang.org/x/sys/unix"
)

func renameNoReplace(old, new string) error {
	err := unix.Renameat2(unix.AT_FDCWD, old, unix.AT_FDCWD, new, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return errNoAtomic // filesystem or kernel without RENAME_NOREPLACE
	}
	return err
}
