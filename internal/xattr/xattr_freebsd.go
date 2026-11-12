//go:build freebsd

package xattr

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/unix"
)

func set(path, name, val string) error {
	if val == "" {
		return nil
	}
	return ignoreUnsupported(setRaw(path, name, val))
}

func setRaw(path, name, val string) error {
	b := []byte(val)
	_, err := unix.ExtattrSetFile(path, unix.EXTATTR_NAMESPACE_USER, name, uintptr(unsafe.Pointer(&b[0])), len(b))
	return err
}

func ignoreUnsupported(err error) error {
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil
	}
	return err
}
