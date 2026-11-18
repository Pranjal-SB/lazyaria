package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
)

const configUsage = "usage: lazyaria config list [--json] | get KEY | set KEY VALUE | reset KEY | edit | path | keys"

// Config runs `lazyaria config list|get|set|reset|edit|path|keys`.
func Config(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(errOut, configUsage)
		return helpExit(args)
	}
	sub, rest := args[0], args[1:]
	want := map[string]int{"list": 0, "keys": 0, "path": 0, "edit": 0, "get": 1, "reset": 1, "set": 2}
	n, ok := want[sub]
	if !ok {
		fmt.Fprintf(errOut, "lazyaria config: unknown subcommand %q\n%s\n", sub, configUsage)
		return exitUsage
	}
	// Values are taken verbatim: "--max-tries=5" and "-1" are values, not flags.
	pos, asJSON := rest, new(bool)
	if sub == "list" || sub == "keys" {
		fs := newFlags("config "+sub, errOut)
		asJSON = fs.Bool("json", false, "print JSON")
		var err error
		if pos, err = parseArgs(fs, rest); err != nil {
			return flagExit(err)
		}
	}
	if len(pos) != n {
		fmt.Fprintln(errOut, configUsage)
		return exitUsage
	}
	switch sub {
	case "path":
		_, p, err := resolveLocal(os.Environ(), nil)
		if err != nil {
			return fail(errOut, "config", err)
		}
		fmt.Fprintln(out, p.ConfigFile)
		return exitOK
	case "edit":
		return fail(errOut, "config edit", configEdit(ctx, out, errOut))
	}
	if sub != "list" && sub != "keys" && !knownKey(pos[0]) {
		return fail(errOut, "config", usagef("unknown setting %q (run `lazyaria config list` to see all keys)", pos[0]))
	}
	if sub == "set" {
		// Same validation as the daemon, so a bad value is a usage error.
		scratch := &config.Settings{Keys: map[string][]string{}, Theme: map[string]string{}}
		if err := config.Set(scratch, pos[0], pos[1]); err != nil {
			return fail(errOut, "config", usageError{err.Error()})
		}
	}

	c, err := connect(ctx)
	if err != nil {
		return fail(errOut, "config", err)
	}
	defer c.Close()
	switch sub {
	case "get":
		e, err := c.ConfigGet(ctx, pos[0])
		if err != nil {
			return fail(errOut, "config", err)
		}
		fmt.Fprintf(out, "%s (%s)\n", fmtValue(maskSetting(e.Key, e.Value)), e.Source)
	case "set":
		if err := c.ConfigSet(ctx, pos[0], pos[1]); err != nil {
			return fail(errOut, "config", err)
		}
		if e, ok := schemaByKey()[pos[0]]; ok && !e.Live {
			fmt.Fprintf(out, "%s saved; it applies after the daemon restarts\n", pos[0])
		}
	case "reset":
		if err := c.ConfigReset(ctx, pos[0]); err != nil {
			return fail(errOut, "config", err)
		}
	default: // list, keys
		ents, err := c.ConfigList(ctx)
		if err != nil {
			return fail(errOut, "config", err)
		}
		if sub == "keys" {
			ents = keyEntries(ents)
		}
		if *asJSON {
			return fail(errOut, "config", writeJSON(out, settingDocs(ents)))
		}
		printSettings(out, ents)
	}
	return exitOK
}

// keyEntries keeps the key bindings.
func keyEntries(ents []ipc.ConfigEntry) []ipc.ConfigEntry {
	var out []ipc.ConfigEntry
	for _, e := range ents {
		if strings.HasPrefix(e.Key, "keys.") {
			out = append(out, e)
		}
	}
	return out
}

func printSettings(out io.Writer, ents []ipc.ConfigEntry) {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, e := range ents {
		restart := ""
		if !e.Live {
			restart = "  (restart)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s%s\n", e.Key, fmtValue(maskSetting(e.Key, e.Value)), e.Source, restart)
	}
	tw.Flush()
}
