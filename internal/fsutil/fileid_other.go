//go:build !unix

package fsutil

import (
	"errors"
	"fmt"
)

// shortcut: no file identity on this platform yet, so ownership checks fail closed; implement with GetFileInformationByHandle when windows is supported.
func FileID(path string) (ino, dev uint64, size int64, err error) {
	return 0, 0, 0, fmt.Errorf("%s: file identity: %w", path, errors.ErrUnsupported)
}
