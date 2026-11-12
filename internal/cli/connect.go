package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
)

// stdin is what `add -` and the `config edit` prompt read; tests swap it.
var stdin io.Reader = os.Stdin

// Exit codes (spec §10).
const (
	exitOK          = 0
	exitRuntime     = 1
	exitUsage       = 2
	exitUnreachable = 3
	exitRefused     = 4
)

// usageError is a bad argument; it exits with code 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// resolveLocal reads the config file, the environment and flags (not the
// DB overrides, which need the paths first) and resolves the paths. The
// file location itself can come from the environment, hence two rounds.
func resolveLocal(env []string, flags map[string]string) (*config.Settings, config.Paths, error) {
	s, _, _, err := config.Load("", env, nil, flags)
	if err != nil {
		return nil, config.Paths{}, err
	}
	p, err := config.ResolvePaths(s, os.Getenv)
	if err != nil {
		return nil, p, err
	}
	if s, _, _, err = config.Load(p.ConfigFile, env, nil, flags); err != nil {
		return nil, p, err
	}
	p, err = config.ResolvePaths(s, os.Getenv)
	return s, p, err
}

// connect reaches the daemon, starting it when daemon.auto_start allows,
// and does the version handshake. An older daemon is asked, once, to
// upgrade itself. A call the daemon cuts off while it re-executes is sent
// again, once, as long as the same daemon process comes back.
func connect(ctx context.Context) (*ipc.Client, error) {
	s, p, err := resolveLocal(os.Environ(), nil)
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	c, err := handshake(ctx, p, s.Daemon.AutoStart, exe)
	if err != nil {
		return nil, err
	}
	pid := lockPID(p)
	c.Redial = func(ctx context.Context) (*ipc.Client, error) {
		// An upgrade keeps the pid and the socket; anything else is a new
		// daemon, and the cut-off call may or may not have run there.
		deadline := time.Now().Add(5 * time.Second)
		for {
			nc, err := handshake(ctx, p, false, exe)
			if err == nil && lockPID(p) != pid {
				nc.Close()
				return nil, errors.New("the daemon was replaced")
			}
			if err == nil || time.Now().After(deadline) {
				return nc, err
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return c, nil
}

// handshake dials and says hello. A daemon that agreed to upgrade is
// redialled until it is back. It is not asked again: if the binary at its
// path is still old, the same version comes back and is used as it is.
func handshake(ctx context.Context, p config.Paths, autoStart bool, exe string) (*ipc.Client, error) {
	c, err := ipc.Connect(ctx, p, autoStart, exe)
	if err != nil {
		return nil, err
	}
	_, upgraded, err := c.Hello(ctx, true)
	if err == nil && !upgraded {
		return c, nil
	}
	c.Close()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		time.Sleep(100 * time.Millisecond)
		if c, err = ipc.Connect(ctx, p, autoStart, exe); err == nil {
			if _, _, err = c.Hello(ctx, false); err == nil {
				return c, nil
			}
			c.Close()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w (the daemon did not come back after upgrading: %w)", ipc.ErrUnreachable, err)
		}
	}
}

// lockPID is the pid in the daemon lock file, or 0.
func lockPID(p config.Paths) int {
	b, _ := os.ReadFile(p.Lock)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// exitCode maps an error to the spec §10 exit codes.
func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	if _, ok := errors.AsType[*daemon.SkippedError](err); ok {
		return exitOK
	}
	if _, ok := errors.AsType[usageError](err); ok {
		return exitUsage
	}
	if _, ok := errors.AsType[engine.ErrInvalidOption](err); ok {
		return exitUsage
	}
	_, foreign := errors.AsType[*ipc.ErrForeignDaemon](err)
	_, mismatch := errors.AsType[*ipc.ErrProtocolMismatch](err)
	if foreign || mismatch || errors.Is(err, ipc.ErrUnreachable) {
		return exitUnreachable
	}
	_, dup := errors.AsType[*daemon.DuplicateError](err)
	_, nospace := errors.AsType[*daemon.NoSpaceError](err)
	_, exists := errors.AsType[*daemon.ExistsError](err)
	if dup || nospace || exists {
		return exitRefused
	}
	return exitRuntime
}

// fail prints err for the named command and returns its exit code.
func fail(errOut io.Writer, cmd string, err error) int {
	if err != nil {
		fmt.Fprintf(errOut, "lazyaria %s: %v\n", cmd, err)
	}
	return exitCode(err)
}

// parseArgs parses flags that may come before, between or after the
// positional arguments, which the flag package alone does not allow.
// shortcut: no "--" terminator, add it if a positional ever starts with "-".
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// newFlags returns a flag set that reports to errOut.
func newFlags(name string, errOut io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("lazyaria "+name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	return fs
}

// flagExit is the exit code for a flag parse error: 0 for -h.
func flagExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	return exitUsage
}
