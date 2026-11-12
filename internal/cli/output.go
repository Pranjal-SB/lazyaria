package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// writeJSON prints one indented JSON document.
func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// humanBytes formats n with IEC units.
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	f, i := float64(n), -1
	for f >= 1024 && i < 5 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %siB", f, "KMGTPE"[i:i+1])
}

// fmtValue shows a config value the way it is typed on the command line.
func fmtValue(v any) string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return `""`
		}
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = fmt.Sprint(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprint(v)
}

// shortIDs returns the shortest prefix length, at least 8, that tells
// every listed job apart.
func shortIDs(jobs []*store.Job) int {
	ids := make([]string, len(jobs))
	for i, j := range jobs {
		ids[i] = j.ID
	}
	slices.Sort(ids)
	n := 8
	for i := 1; i < len(ids); i++ {
		for n < len(ids[i]) && strings.HasPrefix(ids[i], ids[i-1][:min(n, len(ids[i-1]))]) {
			n++
		}
	}
	return n
}

// printJobs writes the human job table.
func printJobs(out io.Writer, jobs []*store.Job) {
	if len(jobs) == 0 {
		fmt.Fprintln(out, "no jobs")
		return
	}
	n := shortIDs(jobs)
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tDONE\tSIZE\tNAME")
	for _, j := range jobs {
		done, size := "-", "-"
		if j.TotalBytes != nil && *j.TotalBytes > 0 {
			size = humanBytes(*j.TotalBytes)
			if j.DoneBytes != nil {
				done = fmt.Sprintf("%d%%", *j.DoneBytes*100 / *j.TotalBytes)
			}
		}
		name := j.Name
		if j.State == store.Error && j.ErrorMsg != nil {
			name += "  (" + *j.ErrorMsg + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", j.ID[:min(n, len(j.ID))], j.State, done, size, name)
	}
	tw.Flush()
}
