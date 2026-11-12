//go:build !windows

package config

import (
	"os"
	"syscall"
)

func ownerUID(fi os.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}

func modeOK(fi os.FileInfo) bool { return fi.Mode().Perm() == 0o700 }
