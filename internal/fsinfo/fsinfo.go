//go:build linux || freebsd || darwin

// Package fsinfo answers small questions about the filesystem behind a directory.
package fsinfo

import "golang.org/x/sys/unix"

// FreeBytes is the space available to an unprivileged user.
func FreeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// Dev returns the device id, used to tell whether two paths share a filesystem.
func Dev(dir string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Dev), nil
}
