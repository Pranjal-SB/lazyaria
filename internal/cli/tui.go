package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/tui"
)

// TUI runs the terminal interface, which `lazyaria` with no command opens
// in a terminal.
func TUI(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) > 0 {
		return fail(errOut, "", usagef("unexpected argument %q", args[0]))
	}
	s, p, err := resolveLocal(os.Environ(), nil)
	if err != nil {
		return fail(errOut, "", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(errOut, "", err)
	}
	dial := func(ctx context.Context) (*ipc.Client, error) {
		c, err := handshake(ctx, p, s.Daemon.AutoStart, exe)
		if err != nil {
			return nil, err
		}
		c.Redial = nil // the TUI reconnects, re-subscribes and re-fetches itself
		return c, nil
	}
	// shortcut: live ui.* changes need a restart, upgrade when the settings screen lands
	if c, err := dial(ctx); err == nil {
		applyOverrides(ctx, c, s)
		c.Close()
	}
	note, err := tui.Run(ctx, tui.Options{
		Dial: func(ctx context.Context) (tui.Daemon, error) {
			c, err := dial(ctx)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
		Settings: s,
		CrashDir: p.StateDir,
		Prog:     Prog,
	})
	if err != nil {
		return fail(errOut, "", err)
	}
	if note != "" {
		fmt.Fprintln(out, note)
	}
	return exitOK
}

// applyOverrides puts the settings changed in the daemon (config set) on
// top of the local ones. A daemon that cannot list them leaves s as it is.
func applyOverrides(ctx context.Context, c *ipc.Client, s *config.Settings) {
	es, err := c.ConfigList(ctx)
	if err != nil {
		return
	}
	for _, e := range es {
		if e.Source == "override" {
			config.Set(s, e.Key, fmtValue(e.Value))
		}
	}
}
