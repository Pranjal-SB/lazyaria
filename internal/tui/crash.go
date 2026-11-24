package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/buildinfo"
)

// guard runs cmd and, if it panics, writes a crash log and panics again,
// so Bubble Tea still restores the terminal.
func guard(cmd tea.Cmd, crashDir string) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		defer logPanic(crashDir)
		return cmd()
	}
}

// logPanic is deferred directly, so its recover sees the panic.
func logPanic(crashDir string) {
	r := recover()
	if r == nil {
		return
	}
	writeCrash(crashDir, r, debug.Stack())
	panic(r)
}

// writeCrash saves <crashDir>/crash-<time>.log. It is best effort: the
// terminal must be restored even when the log cannot be written.
func writeCrash(crashDir string, r any, stack []byte) {
	if crashDir == "" || os.MkdirAll(crashDir, 0o700) != nil {
		return
	}
	name := filepath.Join(crashDir, "crash-"+time.Now().Format("20060102-150405.000000")+".log")
	body := fmt.Sprintf("lazyaria %s on %s/%s, TERM=%s\n\npanic: %v\n\n%s",
		buildinfo.Version, runtime.GOOS, runtime.GOARCH, os.Getenv("TERM"), r, stack)
	os.WriteFile(name, []byte(body), 0o600)
}
