package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

// idleCheck is how often the loop looks at the job count when nothing
// wakes it sooner.
const idleCheck = 250 * time.Millisecond

// loop owns the idle timer and the sleep inhibitor, so both are only ever
// touched from this goroutine. It returns when the daemon should stop:
// upgrade is true when a client asked for daemon.upgrade.
func (d *daemonProc) loop(ctx context.Context) (upgrade bool) {
	tick := time.NewTicker(idleCheck)
	defer tick.Stop()
	inh := &inhibit{d: d}
	defer inh.release()
	var idle *daemon.Idle
	idleFor := time.Duration(-1)
	for {
		s := d.cfg.Load()
		if after := idleExit(s); after != idleFor {
			idle, idleFor = daemon.NewIdle(after, nil), after
		}
		n, err := d.m.ActiveCount()
		if err != nil {
			// Unknown is not idle, and the inhibitor keeps its last state.
			d.log.Warn("count active jobs", "err", err)
			n = 1
		} else {
			inh.update(s, n)
		}
		if at := idle.Update(n, int(d.clients.Load()), s.Daemon.Mode == "always"); at != nil && !time.Now().Before(*at) {
			d.log.Info("idle, shutting down", "idle_exit", idleFor)
			return false
		}
		select {
		case <-ctx.Done():
			d.log.Info("signal received, shutting down")
			return false
		case <-d.hup:
			if err := d.reload(ctx); err != nil {
				d.log.Error("reload config on SIGHUP", "err", err)
			} else {
				d.log.Info("config reloaded")
			}
		case <-d.upgradeReq:
			return true
		case <-tick.C:
		case <-d.wake:
		}
	}
}

func idleExit(s *config.Settings) time.Duration {
	d, err := config.ParseDuration(s.Daemon.IdleExit)
	if err != nil || d <= 0 {
		return 30 * time.Second
	}
	return d
}

// inhibit holds the logind sleep lock while jobs have work to do.
type inhibit struct {
	d    *daemonProc
	i    daemon.Inhibitor
	mode string
	n    int // job count the lock was last taken (or tried) for
}

func (h *inhibit) update(s *config.Settings, n int) {
	if !s.Reliability.InhibitSleep {
		n = 0
	}
	if h.i != nil && h.mode != s.Reliability.InhibitMode {
		h.release()
	}
	// Retrying only when the count changes keeps a missing logind from
	// being asked, and logged, four times a second.
	if n == h.n {
		return
	}
	if h.n = n; n == 0 {
		h.release()
		return
	}
	if h.i == nil {
		h.i, h.mode = daemon.NewInhibitor(s.Reliability.InhibitMode), s.Reliability.InhibitMode
	}
	if err := h.i.Hold(fmt.Sprintf("Downloading %d files", n)); err != nil {
		h.d.log.Warn("cannot keep the machine awake; run `lazyaria doctor`", "err", err)
		h.d.inhibiting.Store(false)
		return
	}
	h.d.inhibiting.Store(true)
}

func (h *inhibit) release() {
	if h.i != nil {
		if err := h.i.Release(); err != nil {
			h.d.log.Warn("release the sleep inhibitor; run `lazyaria doctor`", "err", err)
		}
	}
	h.i, h.n = nil, 0
	h.d.inhibiting.Store(false)
}
