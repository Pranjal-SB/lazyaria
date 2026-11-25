package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// showCells is the width of the piece map `show` prints.
const showCells = 40

// Show runs `lazyaria show [--json] <job>`: one job's files, sources,
// piece map and recent history.
func Show(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := newFlags("show", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if len(pos) != 1 {
		return fail(errOut, "show", usagef("give one job number like 14 (see `%s list`)", Prog))
	}
	c, err := connect(ctx)
	if err != nil {
		return fail(errOut, "show", err)
	}
	defer c.Close()
	ids, _, err := resolveIDs(ctx, c, pos)
	if err != nil {
		return fail(errOut, "show", err)
	}
	d, err := c.Details(ctx, ids[0], showCells)
	if err != nil {
		return fail(errOut, "show", err)
	}
	if d.Job == nil {
		return fail(errOut, "show", fmt.Errorf("the daemon sent no job; see `%s logs`", Prog))
	}
	maskJobs([]*store.Job{d.Job})
	if *asJSON {
		return fail(errOut, "show", writeJSON(out, d))
	}
	printDetails(out, d)
	return exitOK
}

func printDetails(out io.Writer, d daemon.Details) {
	j := d.Job
	fmt.Fprintf(out, "%s %s\n", numRef(j), j.Name)
	row := func(k, v string) { fmt.Fprintf(out, "  %-9s %s\n", k, v) }
	state := j.Label
	if state == "" {
		state = string(j.State)
	}
	if j.ErrorMsg != nil {
		state += ": " + *j.ErrorMsg
	}
	row("state", state)
	if j.TotalBytes != nil && *j.TotalBytes > 0 {
		done := int64(0)
		if j.DoneBytes != nil {
			done = *j.DoneBytes
		}
		row("progress", fmt.Sprintf("%d%%, %s of %s", done*100 / *j.TotalBytes, textutil.Bytes(done, false), textutil.Bytes(*j.TotalBytes, false)))
	}
	if j.SpeedDown > 0 || j.SpeedUp > 0 {
		row("speed", fmt.Sprintf("%s/s down, %s/s up", textutil.Bytes(j.SpeedDown, false), textutil.Bytes(j.SpeedUp, false)))
	}
	if j.ETASeconds != nil {
		row("left", textutil.Duration(time.Duration(*j.ETASeconds)*time.Second))
	}
	row("folder", j.Dir)
	for _, u := range j.URIs {
		row("link", u)
	}
	if d.Segments != nil {
		count := ""
		if d.Pieces > 0 { // a finished job's map comes without a count
			count = fmt.Sprintf(" %d pieces", d.Pieces)
		}
		row("pieces", pieceMap(d.Segments)+count)
	}
	if d.Peers > 0 {
		row("peers", fmt.Sprint(d.Peers))
	}
	for _, s := range d.Servers {
		row("server", maskURL(s))
	}
	if len(d.Files) > 0 {
		fmt.Fprintln(out, "files")
		for _, f := range d.Files {
			pct := 0
			if f.Length > 0 {
				pct = int(f.Done * 100 / f.Length)
			}
			skip := ""
			if !f.Selected {
				skip = "  (skipped)"
			}
			fmt.Fprintf(out, "  %3d%%  %9s  %s%s\n", pct, textutil.Bytes(f.Length, false), f.Path, skip)
		}
	}
	if len(d.Events) > 0 {
		fmt.Fprintln(out, "history")
		for _, e := range d.Events {
			line := e.At.Format("2006-01-02 15:04:05") + "  " + e.Kind
			if e.Detail != "" {
				line += "  " + e.Detail
			}
			fmt.Fprintln(out, "  "+line)
		}
	}
}

// pieceMap draws cells as done, started or not yet (decision: █▒░).
func pieceMap(cells []int) string {
	var b strings.Builder
	for _, v := range cells {
		switch {
		case v >= 100:
			b.WriteString("█")
		case v > 0:
			b.WriteString("▒")
		default:
			b.WriteString("░")
		}
	}
	return b.String()
}
