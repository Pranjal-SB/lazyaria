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
	"syscall"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/ipc"
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
// service that keeps the daemon running, for people who want it always on.
func Service(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) != 1 || (args[0] != "install" && args[0] != "uninstall") {
		fmt.Fprintln(errOut, "usage: lazyaria service install|uninstall")
		return helpExit(args)
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

	// A daemon already holding the lock would make the service's daemon
	// lose the race and exit 0, which Restart=on-failure never retries.
	if err := stopRunningDaemon(ctx, out); err != nil {
		return fail(errOut, "service", err)
	}
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		return fail(errOut, "service", err)
	}
	if err := os.WriteFile(unit, []byte(fmt.Sprintf(unitTemplate, unitExec(exe))), 0o644); err != nil {
		return fail(errOut, "service", err)
	}
	// restart, not start: a reinstall may point at a new executable.
	for _, a := range [][]string{{"daemon-reload"}, {"enable", "lazyaria"}, {"restart", "lazyaria"}} {
		if err := systemctl(ctx, errOut, a...); err != nil {
			return fail(errOut, "service", err)
		}
	}
	time.Sleep(settleDelay)
	if systemctl(ctx, io.Discard, "is-active", "--quiet", "lazyaria") != nil {
		return fail(errOut, "service", fmt.Errorf("installed %s, but the service is not running; see `journalctl --user -u lazyaria`", unit))
	}
	fmt.Fprintf(out, "installed %s and started it; logs: journalctl --user -u lazyaria\n", unit)
	return exitOK
}

// stopRunningDaemon ends a daemon started on demand so the service can
// take over. Its jobs live in the DB, so the service's daemon resumes them.
func stopRunningDaemon(ctx context.Context, out io.Writer) error {
	_, p, err := resolveLocal(os.Environ(), nil)
	if err != nil {
		return err
	}
	c, err := ipc.Dial(ctx, p)
	if err != nil {
		return nil // nothing running
	}
	st, err := c.Status(ctx)
	c.Close()
	if err != nil {
		return err
	}
	if st.PID <= 0 {
		return errors.New("the running daemon did not report its pid; stop it and try again")
	}
	if err := syscall.Kill(st.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop the running daemon (pid %d): %w", st.PID, err)
	}
	fmt.Fprintln(out, "stopped the running daemon; running downloads continue under the service")
	// The lock goes with the process, so wait for the process, not the socket.
	deadline := time.Now().Add(stopTimeout)
	for syscall.Kill(st.PID, 0) == nil {
		if time.Now().After(deadline) {
			return fmt.Errorf("the running daemon (pid %d) did not exit within %s; stop it and try again", st.PID, stopTimeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
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

const stopTimeout = 10 * time.Second

// settleDelay gives a just-started service time to fail; tests shorten it.
var settleDelay = time.Second

// unitExec quotes the executable path for ExecStart. systemd expands %
// specifiers there, so a literal % is doubled.
func unitExec(exe string) string {
	exe = strings.ReplaceAll(exe, "%", "%%")
	if strings.ContainsAny(exe, " \t\"\\") {
		exe = strconv.Quote(exe)
	}
	return exe
}
