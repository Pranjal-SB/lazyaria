package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/logging"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
)

// daemonProc is one running daemon and everything it owns.
type daemonProc struct {
	mode    string   // --mode, "" leaves daemon.mode alone
	handoff *handoff // set when a previous image of this daemon exec'd us

	paths  config.Paths
	env    []string
	flags  map[string]string
	log    *slog.Logger
	level  slog.LevelVar
	redact atomic.Bool

	// cfg is what the daemon runs with: restart-only keys as at start, live
	// keys as of the last reload. Readers on any goroutine Load it.
	cfg      atomic.Pointer[config.Settings]
	entries  atomic.Pointer[[]config.Entry]
	reloadMu sync.Mutex

	l    net.Listener
	lock *ipc.Lock
	st   *store.Store
	sv   *supervisor.Supervisor // nil when attached to someone else's aria2
	eng  engine.Engine
	m    *daemon.Manager
	bus  *daemon.Bus

	started    time.Time
	ariaVer    string
	engState   atomic.Value // string phase
	clients    atomic.Int64
	inhibiting atomic.Bool
	hup        chan os.Signal
	wake       chan struct{}
	upgradeReq chan struct{}
}

// run starts the daemon, calls ready once it serves, and blocks until it
// exits. An error means start-up failed.
func (d *daemonProc) run(ctx context.Context, ready func()) (int, error) {
	// Before anything else: SIGHUP's default action would kill us.
	d.hup = make(chan os.Signal, 1)
	signal.Notify(d.hup, syscall.SIGHUP)
	defer signal.Stop(d.hup)
	d.started, d.env = time.Now(), os.Environ()
	d.wake, d.upgradeReq = make(chan struct{}, 1), make(chan struct{}, 1)
	d.flags = map[string]string{}
	if d.mode != "" {
		d.flags["daemon.mode"] = d.mode
	}

	pre, err := d.resolve()
	if err != nil {
		return exitCode(err), err
	}
	closeLog := d.openLog(pre)
	defer closeLog()
	supervised := pre.Engine.AttachURL == ""
	if d.handoff != nil {
		d.l, d.lock, err = ipc.Adopt(d.handoff.ListenFD, d.handoff.LockFD)
		if err == nil && supervised {
			// Only now: the inherited fds are close-on-exec again.
			d.ariaVer, _ = aria2.CheckVersion(pre.Engine.Aria2cPath)
		}
	} else {
		if supervised {
			// Fail before the socket exists, so the client that started us
			// reports how to install aria2 instead of hanging.
			if d.ariaVer, err = aria2.CheckVersion(pre.Engine.Aria2cPath); err != nil {
				return 1, err
			}
		}
		d.l, d.lock, err = ipc.Listen(d.paths)
	}
	if err != nil {
		return 1, err
	}
	defer d.lock.Close()
	if d.st, err = store.Open(d.paths.DB); err != nil {
		d.l.Close()
		return 1, fmt.Errorf("open %s: %w; run `lazyaria doctor`", d.paths.DB, err)
	}
	s, ents, err := d.load()
	if err != nil {
		d.l.Close()
		d.st.Close()
		return 1, err
	}
	d.cfg.Store(s)
	d.entries.Store(&ents)
	d.redact.Store(s.Privacy.RedactLogs)
	d.level.Set(logLevel(s.Log.Level))

	d.bus = daemon.NewBus(0)
	if err := d.startEngine(ctx, s); err != nil {
		d.l.Close()
		d.st.Close()
		if ctx.Err() != nil { // stopped by a signal while aria2 was starting
			return 0, nil
		}
		return 1, err
	}
	d.m = daemon.NewManager(d.eng, d.st, s, d.bus, d.log)
	d.m.Attach = s.Engine.AttachURL != ""
	runCtx, stopRun := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { d.m.Run(runCtx); close(runDone) }()
	serveCtx, stopServe := context.WithCancel(context.Background())
	serveDone := make(chan struct{})
	go func() { ipc.Serve(serveCtx, d.l, handler{Manager: d.m, d: d}); close(serveDone) }()
	ready()
	d.log.Info("daemon started", "pid", os.Getpid(), "mode", s.Daemon.Mode, "socket", d.paths.Socket, "upgraded", d.handoff != nil)

	upgrade := d.loop(ctx)
	var next *upgradeState
	if upgrade {
		next = d.prepareUpgrade()
	}
	stopServe()
	<-serveDone
	stopRun()
	<-runDone
	if next != nil {
		d.st.Close()
		err := next.exec()
		// Only reached when the exec failed: shut down for real, and the
		// next client starts a fresh daemon.
		d.log.Error("upgrade failed; shutting down instead", "err", err)
		d.st = nil
	}
	d.shutdown()
	if upgrade { // the upgrade did not happen
		return 1, nil
	}
	return 0, nil
}

// resolve finds the paths from the config file, the environment and the
// flags (the runtime overrides live in the DB, which needs the paths) and
// creates the state and runtime dirs.
func (d *daemonProc) resolve() (*config.Settings, error) {
	s, p, err := resolveLocal(d.env, d.flags)
	if err != nil {
		return nil, err
	}
	d.paths = p
	return s, config.EnsureDirs(d.paths)
}

func (d *daemonProc) openLog(s *config.Settings) func() {
	d.redact.Store(s.Privacy.RedactLogs)
	d.level.Set(logLevel(s.Log.Level))
	size, _ := config.ParseSize(s.Log.MaxSize)
	log, closer := logging.New(logging.Options{File: d.paths.Log, MaxSize: size, Keep: s.Log.Keep, Level: &d.level, Redact: &d.redact})
	d.log = log
	slog.SetDefault(log)
	return func() { closer.Close() }
}

func logLevel(s string) slog.Level {
	var l slog.Level
	if l.UnmarshalText([]byte(s)) != nil {
		return slog.LevelInfo
	}
	return l
}

// shutdown stops aria2, closes the DB and removes the socket. The lock is
// released last, by run's defer.
func (d *daemonProc) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := d.eng.Shutdown(ctx); err != nil {
		d.log.Warn("aria2 shutdown", "err", err)
	}
	if d.st != nil {
		if err := d.st.Close(); err != nil {
			d.log.Warn("close database", "err", err)
		}
	}
	// An adopted listener does not unlink on close, so do it here.
	if fi, err := os.Lstat(d.paths.Socket); err == nil && fi.Mode().Type() == os.ModeSocket {
		os.Remove(d.paths.Socket)
	}
	d.log.Info("daemon stopped")
}

func (d *daemonProc) kick() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}
