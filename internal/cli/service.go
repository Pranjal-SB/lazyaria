package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// KillMode=mixed: systemd sends SIGTERM only to the daemon, which stops
// its aria2c itself; the rest of the group gets SIGKILL only at the end.
const unitTemplate = `[Unit]
Description=lazyaria download daemon
After=network-online.target

[Service]
Type=simple
ExecStart=%s daemon --mode=always
Restart=on-failure
KillMode=mixed

[Install]
WantedBy=default.target
`

// Service runs `lazyaria service install|uninstall`, a systemd user
// service that keeps the daemon running (spec §17 G18).
func Service(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) != 1 || (args[0] != "install" && args[0] != "uninstall") {
		fmt.Fprintln(errOut, "usage: lazyaria service install|uninstall")
		return exitUsage
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return fail(errOut, "service", err)
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		fmt.Fprintf(out, "systemctl not found, so there is no systemd user service to %s.\n"+
			"To keep the daemon running, start this at login from your init system or session:\n  %s daemon --mode=always\n", args[0], exe)
		return exitRuntime
	}
	unit := filepath.Join(configHome(), "systemd", "user", "lazyaria.service")
	if args[0] == "uninstall" {
		err := systemctl(ctx, errOut, "disable", "--now", "lazyaria")
		if rerr := os.Remove(unit); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return fail(errOut, "service", rerr)
		}
		if err == nil {
			err = systemctl(ctx, errOut, "daemon-reload")
		}
		if err != nil {
			return fail(errOut, "service", err)
		}
		fmt.Fprintf(out, "removed %s\n", unit)
		return exitOK
	}

	if strings.ContainsAny(exe, " \t\"\\") {
		exe = strconv.Quote(exe)
	}
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		return fail(errOut, "service", err)
	}
	if err := os.WriteFile(unit, []byte(fmt.Sprintf(unitTemplate, exe)), 0o644); err != nil {
		return fail(errOut, "service", err)
	}
	for _, a := range [][]string{{"daemon-reload"}, {"enable", "--now", "lazyaria"}} {
		if err := systemctl(ctx, errOut, a...); err != nil {
			return fail(errOut, "service", err)
		}
	}
	fmt.Fprintf(out, "installed %s and started it; logs: journalctl --user -u lazyaria\n", unit)
	return exitOK
}

func configHome() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

func systemctl(ctx context.Context, errOut io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout, cmd.Stderr = errOut, errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl --user %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
