package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"
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
	var kept []string
	code := jobAction(ctx, "remove", args, out, errOut,
		func(fs *flag.FlagSet) { del = fs.Bool("delete-files", false, "also delete the downloaded files") },
		func(c *ipc.Client, ids []string) (err error) {
			tok, kept, err = c.Remove(ctx, ids, *del)
			return err
		})
	for _, p := range kept {
		fmt.Fprintf(out, "kept %s: cannot tell which files are this torrent's\n", p)
	}
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
	ids, jobs, err := resolveIDs(ctx, c, pos)
	if err != nil {
		return fail(errOut, name, err)
	}
	if err := do(c, ids); err != nil {
		return fail(errOut, name, err)
	}
	for _, id := range ids {
		if why := unchanged(name, jobs, id); why != "" {
			fmt.Fprintf(out, "%s %s; nothing to %s\n", id, why, name)
			continue
		}
		fmt.Fprintf(out, "%s %s\n", pastTense[name], id)
	}
	return exitOK
}

// unchanged says why pause or resume leaves job id as it was, as the
// daemon decides it (daemon.Manager pause and resume), or "" if it acts.
func unchanged(action string, jobs []*store.Job, id string) string {
	i := slices.IndexFunc(jobs, func(j *store.Job) bool { return j.ID == id })
	if i < 0 || (action != "pause" && action != "resume") {
		return ""
	}
	j := jobs[i]
	switch {
	case j.State == store.Done:
		return "is already finished"
	case j.State == store.Seeding:
		return "is seeding"
	case action == "pause" && j.State == store.Paused:
		return "is already paused"
	case action == "pause" && j.State == store.Error:
		return "has failed"
	case action == "resume" && j.State == store.Active:
		return "is already running"
	case action == "resume" && j.State == store.Queued && j.EngineRef != nil:
		return "is already queued"
	}
	return ""
}

var pastTense = map[string]string{"pause": "paused", "resume": "resumed", "remove": "removed", "refresh": "refreshed"}

// resolveIDs maps each prefix to the one job id it starts. Case does not
// matter: ids are upper-case ULIDs. The daemon does the matching, so the
// whole history is never fetched. The jobs it matched come back too.
func resolveIDs(ctx context.Context, c *ipc.Client, prefixes []string) ([]string, []*store.Job, error) {
	upper := make([]string, len(prefixes))
	for i, p := range prefixes {
		if len(p) < minPrefix {
			return nil, nil, usagef("id prefix %q is too short; give at least %d characters", p, minPrefix)
		}
		upper[i] = strings.ToUpper(p)
	}
	jobs, err := c.Match(ctx, upper...)
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	for _, p := range prefixes {
		var match []string
		for _, j := range jobs {
			if strings.HasPrefix(j.ID, strings.ToUpper(p)) {
				match = append(match, j.ID)
			}
		}
		switch len(match) {
		case 0:
			return nil, nil, usagef("no job id starts with %q; removed jobs are not listed, the others are in `lazyaria list --all`", p)
		case 1:
			ids = append(ids, match[0])
		default:
			return nil, nil, usagef("id prefix %q matches %d jobs, give more characters:\n  %s", p, len(match), strings.Join(match, "\n  "))
		}
	}
	return ids, jobs, nil
}
