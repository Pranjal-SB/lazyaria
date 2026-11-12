//go:build freebsd || darwin

package fsinfo

import "golang.org/x/sys/unix"

// Allocation is always "none" here; falloc is not reliable on these systems.
func Allocation(string) string { return "none" }

// IsFAT reports whether dir lives on a FAT/exFAT volume, which restricts file names.
func IsFAT(dir string) bool {
	var st unix.Statfs_t
	if unix.Statfs(dir, &st) != nil {
		return false
	}
	switch unix.ByteSliceToString(st.Fstypename[:]) {
	case "msdosfs", "exfat":
		return true
	}
	return false
}

// TypeName names the filesystem dir lives on.
func TypeName(dir string) string {
	var st unix.Statfs_t
	if unix.Statfs(dir, &st) != nil {
		return "unknown"
	}
	return unix.ByteSliceToString(st.Fstypename[:])
}
