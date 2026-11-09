package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Pranjal-SB/lazyaria/internal/buildinfo"
	"github.com/Pranjal-SB/lazyaria/internal/cli"
)

// commands maps a subcommand name to its handler. Subcommands register here.
// The help text is static on purpose: a help handler that ranged over this map
// would be an initialization cycle.
var commands = map[string]func(ctx context.Context, args []string, out, errOut io.Writer) int{
	"version": cmdVersion,
	"help":    cmdHelp,
	"daemon":  cli.Daemon,
}

const usage = `usage: lazyaria <command> [args]

commands:
  daemon    run the daemon (normally started automatically)
            --detach  run in the background
            --mode    on-demand or always
  version   print the version and IPC protocol
  help      show this help
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// After the first signal the default action is back, so a second one
	// ends a slow shutdown at once.
	context.AfterFunc(ctx, stop)
	code := run(ctx, os.Args, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, ok := commands[args[1]]
	if !ok {
		fmt.Fprintf(stderr, "lazyaria: unknown command %q. Run \"lazyaria help\" to see the commands.\n", args[1])
		return 2
	}
	return cmd(ctx, args[2:], stdout, stderr)
}

func cmdVersion(_ context.Context, _ []string, out, _ io.Writer) int {
	fmt.Fprintf(out, "lazyaria %s (protocol %d)\n", buildinfo.Version, buildinfo.Protocol)
	return 0
}

func cmdHelp(_ context.Context, _ []string, out, _ io.Writer) int {
	fmt.Fprint(out, usage)
	return 0
}
