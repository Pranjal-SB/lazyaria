package cli

import (
	"context"
	"fmt"
	"io"
	"time"
)

// List runs `lazyaria list [--json] [--all]`.
func List(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := newFlags("list", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	all := fs.Bool("all", false, "include finished and removed jobs")
	if _, err := parseArgs(fs, args); err != nil {
		return flagExit(err)
	}
	c, err := connect(ctx)
	if err != nil {
		return fail(errOut, "list", err)
	}
	defer c.Close()
	r, err := c.List(ctx, *all, 0)
	if err != nil {
		return fail(errOut, "list", err)
	}
	if *asJSON {
		return fail(errOut, "list", writeJSON(out, listDoc{Jobs: r.Jobs}))
	}
	printJobs(out, r.Jobs)
	return exitOK
}

// Status runs `lazyaria status [--json]`.
func Status(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := newFlags("status", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return flagExit(err)
	}
	c, err := connect(ctx)
	if err != nil {
		return fail(errOut, "status", err)
	}
	defer c.Close()
	h, _, err := c.Hello(ctx)
	if err != nil {
		return fail(errOut, "status", err)
	}
	st, err := c.Status(ctx)
	if err != nil {
		return fail(errOut, "status", err)
	}
	if *asJSON {
		return fail(errOut, "status", writeJSON(out, statusDoc{DaemonVersion: h.DaemonVersion, Protocol: h.Protocol, Status: st}))
	}
	fmt.Fprintf(out, "daemon     %s (protocol %d), pid %d, mode %s, up %s\n", h.DaemonVersion, h.Protocol, st.PID, st.Mode, time.Duration(st.Uptime)*time.Second)
	fmt.Fprintf(out, "engine     %s, aria2 %s\n", st.EngineState, st.Aria2Version)
	fmt.Fprintf(out, "downloads  %s\n", st.DownloadDir)
	if st.InhibitingSleep {
		fmt.Fprintln(out, "sleep      held off while downloading")
	}
	return exitOK
}
