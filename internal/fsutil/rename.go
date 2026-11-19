// Package fsutil holds file operations that never overwrite.
package fsutil

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// errNoAtomic means the platform or filesystem has no atomic no-replace rename.
var errNoAtomic = errors.New("no atomic no-replace rename")

var renameAtomic = renameNoReplace // swapped in tests to force the fallback

// RenameNoReplace renames old to new and fails with an error matching
// fs.ErrExist when new exists. Linux uses renameat2(RENAME_NOREPLACE), macOS
// renamex_np(RENAME_EXCL).
func RenameNoReplace(old, new string) error {
	err := renameAtomic(old, new)
	if !errors.Is(err, errNoAtomic) {
		if err != nil {
			return &os.LinkError{Op: "rename", Old: old, New: new, Err: err}
		}
		return nil
	}
	// shortcut: Lstat then Rename races another writer creating new in between; upgrade when the platform gains an atomic no-replace rename.
	if _, err := os.Lstat(new); err == nil {
		return &os.LinkError{Op: "rename", Old: old, New: new, Err: fs.ErrExist}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Rename(old, new)
}

// IsCrossDevice reports whether err is EXDEV.
func IsCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }
