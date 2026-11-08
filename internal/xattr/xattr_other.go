//go:build !linux && !darwin && !freebsd

package xattr

func set(path, name, val string) error { return nil }
