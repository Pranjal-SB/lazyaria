// Package cli holds the lazyaria subcommands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/Pranjal-SB/lazyaria/internal/ipc"
)

// statusFDEnv names the fd a detached daemon reports its start-up result on.
const statusFDEnv = "LAZYARIA_STATUS_FD"

// Daemon runs `lazyaria daemon [--detach] [--mode on-demand|always]`.
func Daemon(ctx context.Context, args []string, _, errOut io.Writer) int {
	fs := newFlags("daemon", errOut)
	detach := fs.Bool("detach", false, "run in the background and return once the daemon is up")
	mode := fs.String("mode", "", "on-demand or always (overrides daemon.mode)")
	if err := fs.Parse(args); err != nil {
		return flagExit(err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errOut, "lazyaria daemon: unexpected argument %q. Run \"lazyaria help\" to see the commands.\n", fs.Arg(0))
		return 2
	}
	if *mode != "" && *mode != "on-demand" && *mode != "always" {
		fmt.Fprintf(errOut, "lazyaria daemon: --mode must be on-demand or always, not %q\n", *mode)
		return 2
	}
	if *detach {
		// Report a bad setting here, with its exit code, rather than as a
		// start-up failure of the background process.
		flags := map[string]string{}
		if *mode != "" {
			flags["daemon.mode"] = *mode
		}
		if _, _, err := resolveLocal(os.Environ(), flags); err != nil {
			return fail(errOut, "daemon", err)
		}
		return detachDaemon(*mode, errOut)
	}

	ho, err := takeHandoff()
	if err != nil {
		fmt.Fprintln(errOut, "lazyaria daemon:", err)
		return 1
	}
	st := takeStatusPipe()
	d := &daemonProc{mode: *mode, handoff: ho}
	code, err := d.run(ctx, st.ready)
	if err != nil {
		if _, lost := errors.AsType[*ipc.ErrAlreadyRunning](err); lost {
			// Losing the race to another daemon is fine: one is serving.
			fmt.Fprintln(errOut, "lazyaria:", err)
			st.ready()
			return 0
		}
		st.fail(err)
		fmt.Fprintln(errOut, "lazyaria daemon:", err)
	}
	return code
}

// statusPipe is the write end the --detach parent reads: "ready", or the
// reason start-up failed.
type statusPipe struct{ f *os.File }

func takeStatusPipe() statusPipe {
	v := os.Getenv(statusFDEnv)
	os.Unsetenv(statusFDEnv)
	fd, err := strconv.Atoi(v)
	if v == "" || err != nil {
		return statusPipe{}
	}
	f := os.NewFile(uintptr(fd), "status")
	setCloseOnExec(fd) // aria2c must not hold the parent's pipe open
	return statusPipe{f: f}
}

func (s statusPipe) ready() { s.send("ready") }

func (s statusPipe) fail(err error) { s.send(err.Error()) }

func (s statusPipe) send(msg string) {
	if s.f != nil {
		s.f.WriteString(msg)
		s.f.Close()
	}
}
