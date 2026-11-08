package trash

import (
	"errors"
	"fmt"
	"os"
)

var (
	renameFn   = os.Rename // swapped in tests to simulate EXDEV
	beforeMove = func() {} // test hook: runs between the exists check and the move
	linkMode   bool        // test hook: force the portable fallback
)

// errNoAtomic means the platform or filesystem has no atomic no-replace rename.
var errNoAtomic = errors.New("no atomic no-replace rename")

// moveNoReplace renames src to dst and fails if dst exists. Linux does this
// atomically with renameat2(RENAME_NOREPLACE), macOS with renamex_np(RENAME_EXCL). Elsewhere regular files use
// link+unlink, which also fails if dst exists. Directories have no atomic
// option there (FreeBSD), so they get Lstat then Rename; rename(2) replaces an
// empty directory created in that window and fails with ENOTEMPTY otherwise.
// It never merges. That race is accepted.
func moveNoReplace(src, dst string) error {
	if !linkMode {
		err := renameNoReplace(src, dst)
		if !errors.Is(err, errNoAtomic) {
			return err
		}
	}
	if fi, err := os.Lstat(src); err == nil && !fi.IsDir() {
		err := os.Link(src, dst)
		if err == nil {
			return os.Remove(src)
		}
		if errors.Is(err, os.ErrExist) {
			return err
		}
		// Filesystem without hard links: fall through to Lstat+Rename.
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %s", os.ErrExist, dst)
	}
	return os.Rename(src, dst)
}
