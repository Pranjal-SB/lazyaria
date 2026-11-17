package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// minPrefix is the shortest id prefix accepted.
const minPrefix = 4

// Pause runs `lazyaria pause <id-prefix>...`.
func Pause(ctx context.Context, args []string, out, errOut io.Writer) int {
	return jobAction(ctx, "pause", args, out, errOut, nil, func(c *ipc.Client, ids []string) error {
		return c.Pause(ctx, ids...)
	})
}

// Resume runs `lazyaria resume <id-prefix>...`.
func Resume(ctx context.Context, args []string, out, errOut io.Writer) int {
	return jobAction(ctx, "resume", args, out, errOut, nil, func(c *ipc.Client, ids []string) error {
		return c.Resume(ctx, ids...)
	})
}

// Remove runs `lazyaria remove [--delete-files] <id-prefix>...` and prints
// the command that undoes it.
func Remove(ctx context.Context, args []string, out, errOut io.Writer) int {
	var del *bool
	var tok string
	code := jobAction(ctx, "remove", args, out, errOut,
		func(fs *flag.FlagSet) { del = fs.Bool("delete-files", false, "also delete the downloaded files") },
		func(c *ipc.Client, ids []string) (err error) {
			tok, err = c.Remove(ctx, ids, *del)
			return err
		})
	// A remove that failed for some jobs may still have removed others.
	if tok != "" {
		fmt.Fprintf(out, "undo: lazyaria undo %s\n", tok)
	}
	return code
}

// Undo runs `lazyaria undo <token>`, which brings back what a recent remove
// took, files included.
func Undo(ctx context.Context, args []string, out, errOut io.Writer) int {
	pos, err := parseArgs(newFlags("undo", errOut), args)
	if err != nil {
		return flagExit(err)
	}
	if len(pos) != 1 {
		return fail(errOut, "undo", usagef("give the token `lazyaria remove` printed: lazyaria undo <token>"))
	}
	c, err := connect(ctx)
	if err != nil {
		return fail(errOut, "undo", err)
	}
	defer c.Close()
	if err := c.Undo(ctx, pos[0]); err != nil {
		return fail(errOut, "undo", err)
	}
	fmt.Fprintln(out, "undone; jobs that were running are paused, resume them with `lazyaria resume`")
	return exitOK
}

// Refresh runs `lazyaria refresh <id-prefix> <link>`: the job carries on
// from a fresh link and keeps what it has downloaded.
func Refresh(ctx context.Context, args []string, out, errOut io.Writer) int {
	pos, err := parseArgs(newFlags("refresh", errOut), args)
	if err != nil {
		return flagExit(err)
	}
	if len(pos) != 2 {
		return fail(errOut, "refresh", usagef("give one job id and its new link: lazyaria refresh <id-prefix> <link>"))
	}
	if err := daemon.CheckLink(pos[1]); err != nil {
		return fail(errOut, "refresh", usagef("%v", err))
	}
	return jobAction(ctx, "refresh", pos[:1], out, errOut, nil, func(c *ipc.Client, ids []string) error {
		_, err := c.Refresh(ctx, ids[0], pos[1])
		return err
	})
}

// jobAction resolves id prefixes and applies do to the matching jobs.
func jobAction(ctx context.Context, name string, args []string, out, errOut io.Writer, flags func(*flag.FlagSet), do func(*ipc.Client, []string) error) int {
	fs := newFlags(name, errOut)
	if flags != nil {
		flags(fs)
	}
	pos, err := parseArgs(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if len(pos) == 0 {
		return fail(errOut, name, usagef("give at least one job id (see `lazyaria list`)"))
	}
	c, err := connect(ctx)
	if err != nil {
		return fail(errOut, name, err)
	}
	defer c.Close()
	r, err := c.List(ctx, true, 0)
	if err != nil {
		return fail(errOut, name, err)
	}
	ids, err := resolveIDs(r.Jobs, pos)
	if err != nil {
		return fail(errOut, name, err)
	}
	if err := do(c, ids); err != nil {
		return fail(errOut, name, err)
	}
	for _, id := range ids {
		fmt.Fprintf(out, "%s %s\n", pastTense[name], id)
	}
	return exitOK
}

var pastTense = map[string]string{"pause": "paused", "resume": "resumed", "remove": "removed", "refresh": "refreshed"}

// resolveIDs maps each prefix to the one job id it starts. Case does not
// matter: ids are upper-case ULIDs.
func resolveIDs(jobs []*store.Job, prefixes []string) ([]string, error) {
	var ids []string
	for _, p := range prefixes {
		if len(p) < minPrefix {
			return nil, usagef("id prefix %q is too short; give at least %d characters", p, minPrefix)
		}
		var match []string
		for _, j := range jobs {
			if strings.HasPrefix(j.ID, strings.ToUpper(p)) {
				match = append(match, j.ID)
			}
		}
		switch len(match) {
		case 0:
			return nil, usagef("no job id starts with %q; see `lazyaria list --all`", p)
		case 1:
			ids = append(ids, match[0])
		default:
			return nil, usagef("id prefix %q matches %d jobs, give more characters:\n  %s", p, len(match), strings.Join(match, "\n  "))
		}
	}
	return ids, nil
}
