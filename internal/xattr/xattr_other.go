//go:build !linux && !darwin && !freebsd

package xattr

import "errors"

func set(path, name, val string) error { return nil }

func setRaw(path, name, val string) error { return errors.ErrUnsupported }
