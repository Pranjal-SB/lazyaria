package main

// The reference pages in docs/reference are generated from the code below
// and the settings schema; docs_test.go fails when they are out of date.
//go:generate go test -count=1 -run ^TestReferenceDocs$ . -args -update-docs

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	"refresh": cli.Refresh,
	"undo":    cli.Undo,
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
  list [--json] [--all] [--limit N]
            show jobs; --all includes finished ones; --limit shows
            at most N
  status [--json]
            daemon and engine status
  pause|resume <job>...
            a job is its number (14 or #14) or the start of its id
  remove [--delete-files] <job>...
            prints an undo command that works for remove.undo_seconds
  undo <token>
            bring back what a remove took, files included
  refresh <job> <link>
            give a download a fresh link and keep its progress
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
	cli.Prog = "lazyaria"
	if len(args) > 0 && filepath.Base(args[0]) == "laria" { // the short alias, decision 25
		cli.Prog = "laria"
	}
	if len(args) < 2 {
		fmt.Fprint(stderr, usageText())
		return 2
	}
	cmd, ok := commands[args[1]]
	if !ok {
		fmt.Fprintf(stderr, "%s: unknown command %q. Run \"%[1]s help\" to see the commands.\n", cli.Prog, args[1])
		return 2
	}
	return cmd(ctx, args[2:], stdout, stderr)
}

// usageText is usage under the name the binary was invoked as.
func usageText() string {
	return strings.Replace(usage, "usage: lazyaria", "usage: "+cli.Prog, 1)
}

func cmdVersion(_ context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet(cli.Prog+" version", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errOut, "%s version: unexpected argument %q. Run \"%[1]s help\" to see the commands.\n", cli.Prog, fs.Arg(0))
		return 2
	}
	fmt.Fprintf(out, "lazyaria %s (protocol %d)\n", buildinfo.Version, buildinfo.Protocol)
	return 0
}

func cmdHelp(_ context.Context, _ []string, out, _ io.Writer) int {
	fmt.Fprint(out, usageText())
	return 0
}
