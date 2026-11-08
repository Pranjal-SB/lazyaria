package trash

import (
	"errors"

	"golang.org/x/sys/unix"
)

func renameNoReplace(src, dst string) error {
	err := unix.RenamexNp(src, dst, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) {
		return errNoAtomic // filesystem without RENAME_EXCL
	}
	return err
}
