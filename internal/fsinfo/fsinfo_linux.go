//go:build linux

package fsinfo

import "golang.org/x/sys/unix"

const exfatMagic = 0x2011BAB0

func fsType(dir string) (int64, bool) {
	var st unix.Statfs_t
	if unix.Statfs(dir, &st) != nil {
		return 0, false
	}
	return int64(st.Type), true
}

// Allocation picks the aria2 file-allocation mode: falloc where the fs supports it.
func Allocation(dir string) string {
	t, ok := fsType(dir)
	if !ok {
		return "none"
	}
	switch t {
	case unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC,
		unix.F2FS_SUPER_MAGIC, unix.TMPFS_MAGIC:
		return "falloc"
	}
	return "none"
}

// IsFAT reports whether dir lives on a FAT/exFAT volume, which restricts file names.
func IsFAT(dir string) bool {
	t, ok := fsType(dir)
	return ok && (t == unix.MSDOS_SUPER_MAGIC || t == exfatMagic)
}
