package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/Pranjal-SB/lazyaria/internal/buildinfo"
)

// commands maps a subcommand name to its handler. Subcommands register here.
// The help text is static on purpose: a help handler that ranged over this map
// would be an initialization cycle.
var commands = map[string]func(ctx context.Context, args []string, out, errOut io.Writer) int{
	"version": cmdVersion,
	"help":    cmdHelp,
}

const usage = `usage: lazyaria <command> [args]

commands:
  version   print the version and IPC protocol
  help      show this help
`

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, ok := commands[args[1]]
	if !ok {
		fmt.Fprintf(stderr, "lazyaria: unknown command %q. Run \"lazyaria help\" to see the commands.\n", args[1])
		return 2
	}
	return cmd(context.Background(), args[2:], stdout, stderr)
}

func cmdVersion(_ context.Context, _ []string, out, _ io.Writer) int {
	fmt.Fprintf(out, "lazyaria %s (protocol %d)\n", buildinfo.Version, buildinfo.Protocol)
	return 0
}

func cmdHelp(_ context.Context, _ []string, out, _ io.Writer) int {
	fmt.Fprint(out, usage)
	return 0
}
