package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
)

// osReleasePath is read for the aria2 install hint; tests point it at a fixture.
var osReleasePath = "/etc/os-release"

// daemonView is what doctor learned from a running daemon.
type daemonView struct {
	pid      int
	dir      string
	settings map[string]any
	jobs     []*store.Job
}

// Doctor runs `lazyaria doctor [--json] [--restart-engine]` (spec §10, G3, G22).
// It exits 1 when any check fails.
func Doctor(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := newFlags("doctor", errOut)
	asJSON := fs.Bool("json", false, "print JSON")
	restart := fs.Bool("restart-engine", false, "restart aria2 with a freshly read config first")
	if err := parseNoArgs(fs, args); err != nil {
		return flagExit(err)
	}
	s, p, err := resolveLocal(os.Environ(), nil)
	if err != nil {
		return fail(errOut, "doctor", err)
	}
	rows := []checkDoc{checkAria2(s), checkRuntimeDir(p), checkSocket(p)}
	d, drows := checkDaemon(ctx, p, *restart)
	rows = append(rows, drows...)
	rows = append(rows, checkDB(p))
	dir := p.DownloadDir
	if d.dir != "" {
		dir = d.dir
	}
	rows = append(rows, checkDownloadDir(dir), checkXattr(dir, s), checkLogind(s), checkTerminal(),
		checkProxy(d, s), checkTLS(d, s))
	if bt, ok := checkBT(d); ok {
		rows = append(rows, bt)
	}
	rows = append(rows, optionalTools()...)

	code := exitOK
	for _, r := range rows {
		if !r.OK {
			code = exitRuntime
		}
	}
	if *asJSON {
		if err := writeJSON(out, rows); err != nil {
			return fail(errOut, "doctor", err)
		}
		return code
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, r := range rows {
		mark := "✓"
		if !r.OK {
			mark = "✗"
		}
		fmt.Fprintf(tw, "%s %s\t%s\n", mark, r.Check, r.Detail)
		if r.Fix != "" {
			fmt.Fprintf(tw, "\tfix: %s\n", r.Fix)
		}
	}
	tw.Flush()
	return code
}

// checkDaemon reports whether the daemon answers and collects what the
// later checks need from it. Only --restart-engine may start a daemon.
func checkDaemon(ctx context.Context, p config.Paths, restart bool) (daemonView, []checkDoc) {
	var (
		c    *ipc.Client
		err  error
		rows []checkDoc
	)
	if restart {
		c, err = connect(ctx) // may start the daemon, which starts aria2
	} else if _, serr := os.Stat(p.Socket); errors.Is(serr, os.ErrNotExist) {
		return daemonView{}, []checkDoc{{Check: "daemon", OK: true, Detail: "not running; it starts when a client needs it"}}
	} else {
		c, err = ipc.Dial(ctx, p)
	}
	if err != nil {
		fix := "see the reason above and `lazyaria logs`"
		if errors.Is(err, syscall.ECONNREFUSED) {
			fix = "no daemon is listening; remove the stale socket " + p.Socket
		}
		return daemonView{}, []checkDoc{{Check: "daemon", Detail: err.Error(), Fix: fix}}
	}
	defer c.Close()
	if restart {
		row := checkDoc{Check: "engine restart", OK: true, Detail: "aria2 restarted with the current config"}
		if err := c.RestartEngine(ctx); err != nil {
			row = checkDoc{Check: "engine restart", Detail: err.Error(), Fix: "fix the cause, then run `lazyaria doctor --restart-engine` again; see `lazyaria logs`"}
		}
		rows = append(rows, row)
	}

	h, _, err := c.Hello(ctx, false)
	if err != nil {
		return daemonView{}, append(rows, checkDoc{Check: "daemon", Detail: err.Error(), Fix: "update lazyaria or let the daemon upgrade when idle"})
	}
	row := checkDoc{Check: "daemon", OK: h.EngineState != supervisor.PhaseFailed,
		Detail: fmt.Sprintf("%s (protocol %d), engine %s", h.DaemonVersion, h.Protocol, h.EngineState)}
	if !row.OK {
		row.Fix = "fix aria2 (see `lazyaria logs`), then run `lazyaria doctor --restart-engine`"
	}
	rows = append(rows, row)

	d := daemonView{settings: map[string]any{}}
	if st, err := c.Status(ctx); err == nil {
		d.pid, d.dir = st.PID, st.DownloadDir
	}
	if ents, err := c.ConfigList(ctx); err == nil {
		for _, e := range ents {
			d.settings[e.Key] = e.Value
		}
	}
	if r, err := c.List(ctx, false, 0); err == nil {
		d.jobs = r.Jobs
	}
	return d, rows
}

// checkBT lists the BitTorrent ports while a torrent is running (G3).
func checkBT(d daemonView) (checkDoc, bool) {
	for _, j := range d.jobs {
		if (j.Kind == "torrent" || j.Kind == "magnet") && (j.State == store.Active || j.State == store.Seeding) {
			ports := []string{"TCP " + fmtValue(d.settings["bt.listen_port"]) + " (bt.listen_port)"}
			if dht, _ := d.settings["bt.enable_dht"].(bool); dht {
				ports = append(ports, "UDP "+fmtValue(d.settings["bt.dht_listen_port"])+" (DHT, bt.dht_listen_port)")
			}
			return checkDoc{Check: "bt ports", OK: true, Detail: "torrents are listening on " + strings.Join(ports, ", "),
				Fix: "allow these ports through your firewall for incoming peers"}, true
		}
	}
	return checkDoc{}, false
}

// checkTLS reports certificate checking, which aria2 only reads at start.
func checkTLS(d daemonView, s *config.Settings) checkDoc {
	on := s.Network.CheckCertificate
	if v, ok := d.settings["network.check_certificate"].(bool); ok {
		on = v
	}
	if on {
		return checkDoc{Check: "tls", OK: true, Detail: "certificates are verified (network.check_certificate; changes apply at daemon restart)"}
	}
	return checkDoc{Check: "tls", Detail: "certificates are NOT verified (network.check_certificate = false)",
		Fix: "`lazyaria config set network.check_certificate true`, then restart the daemon; this key is restart-only"}
}
