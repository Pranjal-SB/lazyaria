//go:build linux || darwin

package xattr

import (
	"errors"

	"golang.org/x/sys/unix"
)

func set(path, name, val string) error { return ignoreUnsupported(setRaw(path, name, val)) }

func setRaw(path, name, val string) error {
	return unix.Setxattr(path, "user."+name, []byte(val), 0)
}

func ignoreUnsupported(err error) error {
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil
	}
	return err
}
