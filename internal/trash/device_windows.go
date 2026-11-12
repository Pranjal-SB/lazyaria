package trash

import "os"

// No freedesktop trash on windows: callers get ErrNoTrash.
func device(os.FileInfo) (uint64, bool) { return 0, false }
