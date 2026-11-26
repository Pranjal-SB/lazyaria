package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
)

// stdin is what `add -` and the `config edit` prompt read; tests swap it.
var stdin io.Reader = os.Stdin

// Exit codes, as documented in docs/reference/exit-codes.md.
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

// Prog is the name the binary was invoked as (lazyaria, or the laria
// alias); help and hints use it. main sets it.
var Prog = "lazyaria"

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
// upgrade itself. After a hang-up (an upgrade, an idle exit, a crash) the
// client reconnects once, starting a daemon as above; only a read-only
// call is sent again (see ipc.Client.Redial).
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
	c.Redial = retry(func(ctx context.Context) (*ipc.Client, error) { return handshake(ctx, p, s.Daemon.AutoStart, exe) })
	return c, nil
}

// retry calls dial until it succeeds or 5 s have passed, for a daemon on
// its way back.
func retry(dial func(context.Context) (*ipc.Client, error)) func(context.Context) (*ipc.Client, error) {
	return func(ctx context.Context) (*ipc.Client, error) {
		deadline := time.Now().Add(5 * time.Second)
		for {
			c, err := dial(ctx)
			if err == nil || time.Now().After(deadline) {
				return c, err
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// handshake dials and says hello. A daemon that agreed to upgrade is
// redialled until it is back. It is not asked again: if the binary at its
// path is still old, the same version comes back and is used as it is.
func handshake(ctx context.Context, p config.Paths, autoStart bool, exe string) (*ipc.Client, error) {
	c, err := ipc.Connect(ctx, p, autoStart, exe)
	if err != nil {
		return nil, err
	}
	// A daemon exiting on idle can hang up on a client it has just
	// accepted; hello is read-only, so it goes again to the next daemon
	// that answers.
	c.Redial = retry(func(ctx context.Context) (*ipc.Client, error) { return greet(ctx, p, autoStart, exe) })
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
		if c, err = greet(ctx, p, autoStart, exe); err == nil {
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w (the daemon did not come back after upgrading: %w)", ipc.ErrUnreachable, err)
		}
	}
}

// greet connects and says hello without asking for an upgrade, so a daemon
// still shutting down, which accepts and then hangs up, counts as down.
func greet(ctx context.Context, p config.Paths, autoStart bool, exe string) (*ipc.Client, error) {
	c, err := ipc.Connect(ctx, p, autoStart, exe)
	if err != nil {
		return nil, err
	}
	if _, _, err = c.Hello(ctx, false); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// exitCode maps an error to the documented exit codes.
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
	if _, ok := errors.AsType[*daemon.BadRequestError](err); ok {
		return exitUsage
	}
	if _, ok := errors.AsType[*config.ValueError](err); ok {
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
		fmt.Fprintf(errOut, "%s %s: %v\n", Prog, cmd, err)
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

// parseNoArgs is parseArgs for a command that takes flags only.
func parseNoArgs(fs *flag.FlagSet, args []string) error {
	pos, err := parseArgs(fs, args)
	if err == nil && len(pos) > 0 {
		err = usagef("unexpected argument %q. Run \"lazyaria help\" to see the commands.", pos[0])
		fmt.Fprintf(fs.Output(), "%s: %v\n", fs.Name(), err)
	}
	return err
}

// newFlags returns a flag set that reports to errOut.
func newFlags(name string, errOut io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(Prog+" "+name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	return fs
}

// isHelp reports whether arg asks for help the way the flag package takes it.
func isHelp(arg string) bool {
	return arg == "-h" || arg == "--h" || arg == "-help" || arg == "--help"
}

// helpExit is the exit code after printing a command's usage: 0 when the
// arguments asked for help.
func helpExit(args []string) int {
	if slices.ContainsFunc(args, isHelp) {
		return exitOK
	}
	return exitUsage
}

// flagExit is the exit code for a flag parse error: 0 for -h.
func flagExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	return exitUsage
}
