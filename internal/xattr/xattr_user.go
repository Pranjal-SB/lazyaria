//go:build linux || darwin

package xattr

import (
	"errors"

	"golang.org/x/sys/unix"
)

func set(path, name, val string) error {
	err := unix.Setxattr(path, "user."+name, []byte(val), 0)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil
	}
	return err
}
