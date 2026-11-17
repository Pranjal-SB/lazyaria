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
	"add":     cli.Add,
	"list":    cli.List,
	"status":  cli.Status,
	"pause":   cli.Pause,
	"resume":  cli.Resume,
	"remove":  cli.Remove,
	"config":  cli.Config,
	"doctor":  cli.Doctor,
	"logs":    cli.Logs,
	"service": cli.Service,
}

const usage = `usage: lazyaria <command> [args]

commands:
  add [--dir D] [--out F] [--paused] [--wait] [--force] <link|file.torrent|->...
            queue downloads; - reads links from stdin; --wait exits 0
            when all finish, 1 if any fails; --force downloads a link
            that is already in the list
  list [--json] [--all]
            show jobs; --all includes finished ones
  status [--json]
            daemon and engine status
  pause|resume <id-prefix>...
  remove [--delete-files] <id-prefix>...
  config list [--json] | get KEY | set KEY VALUE | reset KEY | edit | path | keys
  doctor [--json] [--restart-engine]
            check the setup and say how to fix what is wrong
  logs [-f]  show the daemon log; -f keeps following it
  service install|uninstall
            run the daemon as a systemd user service
  daemon [--detach] [--mode on-demand|always]
            run the daemon (normally started automatically)
  version   print the version and IPC protocol
  help      show this help

exit codes: 0 ok, 1 error, 2 usage, 3 daemon unreachable, 4 duplicate or refused
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
