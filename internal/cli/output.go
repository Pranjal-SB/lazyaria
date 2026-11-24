package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
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

// printJobs writes the human job table.
func printJobs(out io.Writer, jobs []*store.Job) {
	if len(jobs) == 0 {
		fmt.Fprintln(out, "no jobs")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSTATE\tDONE\tSIZE\tNAME")
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", numRef(j), j.State, done, size, name)
	}
	tw.Flush()
}

// maskURL hides the userinfo of a URL.
func maskURL(v string) string {
	u, err := url.Parse(v)
	if err != nil {
		return textutil.StripUserinfo(v, "***@")
	}
	if u.User == nil {
		return v
	}
	u.User = nil // url.User("***") would print as %2A%2A%2A
	return strings.Replace(u.String(), "://", "://***@", 1)
}

// maskProxy is maskURL that also takes a bare user:pass@host:port.
func maskProxy(v string) string {
	if u, err := url.Parse(v); err == nil && u.Host != "" {
		return maskURL(v)
	}
	at := strings.LastIndex(v, "@")
	if at < 0 {
		return v
	}
	start := 0
	if i := strings.Index(v, "://"); i >= 0 && i < at {
		start = i + 3
	}
	return v[:start] + "***" + v[at:]
}

// maskJobs hides credentials left in job links. The daemon already masks
// auth options and moves userinfo out of links; this covers older rows.
func maskJobs(jobs []*store.Job) {
	for _, j := range jobs {
		for i, u := range j.URIs {
			j.URIs[i] = maskURL(u)
		}
		if j.Referrer != nil {
			r := maskURL(*j.Referrer)
			j.Referrer = &r
		}
	}
}

// urlSettings hold URLs that may carry credentials.
var urlSettings = map[string]bool{"network.all_proxy": true, "engine.attach_url": true}

// maskSetting hides credentials in a setting's value.
func maskSetting(key string, v any) any {
	if s, ok := v.(string); ok && urlSettings[key] {
		return maskProxy(s)
	}
	return v
}
