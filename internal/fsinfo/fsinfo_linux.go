//go:build linux

package fsinfo

import (
	"fmt"

	"golang.org/x/sys/unix"
)

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

var typeNames = map[int64]string{
	unix.EXT4_SUPER_MAGIC: "ext4", unix.XFS_SUPER_MAGIC: "xfs", unix.BTRFS_SUPER_MAGIC: "btrfs",
	unix.F2FS_SUPER_MAGIC: "f2fs", unix.TMPFS_MAGIC: "tmpfs", unix.MSDOS_SUPER_MAGIC: "vfat",
	exfatMagic: "exfat", unix.NFS_SUPER_MAGIC: "nfs", unix.FUSE_SUPER_MAGIC: "fuse",
	unix.OVERLAYFS_SUPER_MAGIC: "overlayfs", 0x5346544e: "ntfs", 0x2fc12fc1: "zfs",
	unix.SMB2_SUPER_MAGIC: "smb", unix.CIFS_SUPER_MAGIC: "cifs",
}

// TypeName names the filesystem dir lives on (ext2 and ext3 share ext4's magic).
func TypeName(dir string) string {
	t, ok := fsType(dir)
	if !ok {
		return "unknown"
	}
	if n, ok := typeNames[t]; ok {
		return n
	}
	return fmt.Sprintf("0x%x", t)
}
