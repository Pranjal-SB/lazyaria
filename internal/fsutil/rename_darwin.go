package fsutil

import (
	"errors"

	"golang.org/x/sys/unix"
)

func renameNoReplace(old, new string) error {
	err := unix.RenamexNp(old, new, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) {
		return errNoAtomic // filesystem without RENAME_EXCL
	}
	return err
}
