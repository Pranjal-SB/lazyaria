//go:build freebsd

package xattr

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/unix"
)

func set(path, name, val string) error {
	b := []byte(val)
	if len(b) == 0 {
		return nil
	}
	_, err := unix.ExtattrSetFile(path, unix.EXTATTR_NAMESPACE_USER, name, uintptr(unsafe.Pointer(&b[0])), len(b))
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil
	}
	return err
}
