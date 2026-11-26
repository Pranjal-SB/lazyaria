package daemon

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points the home trash into a temp folder on the same disk as
// the tests' files. A test that trashes without its own XDG_DATA_HOME
// would otherwise fill the real home trash, or a shared .Trash-<uid>
// folder at the top of the temp disk.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lazyaria-trash")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("XDG_DATA_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
