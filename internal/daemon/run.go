package daemon

import (
	"context"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

const purgeEvery = 24 * time.Hour

// Run purges old removed jobs, reconciles, then handles engine events,
// polling and retry timers until ctx ends.
func (m *Manager) Run(ctx context.Context) error {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	defer m.stopRetries()
	m.purge(ctx)
	if err := m.Reconcile(ctx); err != nil {
		m.log.Warn("reconcile at start", "err", err)
	}
	purge := time.NewTicker(purgeEvery)
	defer purge.Stop()
	events := m.e.Events()
	last := time.Now()
	for {
		var pollC <-chan time.Time
		var t *time.Timer
		if d := m.pollEvery(ctx); d > 0 {
			t = time.NewTimer(max(time.Until(last.Add(d)), 0))
			pollC = t.C
		}
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				events = nil // engine shut down; keep serving until ctx ends
				break
			}
			m.handleEvent(ctx, ev)
		case <-pollC:
			m.poll(ctx)
			last = time.Now()
		case <-m.kick:
		case <-purge.C:
			m.purge(ctx)
		}
		if t != nil {
			t.Stop()
		}
	}
}

// pollEvery is the poll period: fast while a client watches progress,
// a slow sentinel otherwise, and none without running jobs (spec §4.5).
func (m *Manager) pollEvery(ctx context.Context) time.Duration {
	js, err := m.st.ListJobs(ctx, store.Filter{States: runningStates})
	if err != nil || len(js) == 0 {
		return 0
	}
	v := m.s.Advanced.SentinelInterval
	if m.observers.Load() > 0 {
		v = m.s.Advanced.PollInterval
	}
	d, err := config.ParseDuration(v)
	if err != nil || d <= 0 {
		d = time.Second
	}
	return d
}

// poll reads every running job in one multicall, applies changes a lost
// notification would have carried, and pushes progress.
func (m *Manager) poll(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	js, err := m.st.ListJobs(ctx, store.Filter{States: runningStates})
	if err != nil {
		m.log.Warn("poll: list jobs", "err", err)
		return
	}
	var refs []engine.EngineRef
	var polled []*store.Job
	for _, j := range js {
		if j.EngineRef != nil {
			refs = append(refs, refOf(j))
			polled = append(polled, j)
		}
	}
	if len(refs) == 0 {
		return
	}
	sts, err := m.e.Status(ctx, refs)
	if err != nil {
		m.log.Debug("poll", "err", err)
		return
	}
	now := m.now()
	var items []ProgressItem
	var down, up int64
	for i, st := range sts {
		if !st.Known {
			continue // Reconcile deals with downloads the engine lost
		}
		j := m.apply(ctx, polled[i], st, "")
		if j.State == store.Active || j.State == store.Queued || j.State == store.Seeding {
			items = append(items, m.prog.observe(j.ID, now, st))
			down += st.Down
			up += st.Up
		}
	}
	m.prog.sample(now, down, up)
	if m.prog.due(now, m.s.Advanced.ProgressHz) {
		m.bus.PublishProgress(items)
	}
}

// purge drops removed rows older than history.purge_removed_after_days;
// 0 keeps them.
func (m *Manager) purge(ctx context.Context) {
	days := m.s.History.PurgeRemovedAfterDays
	if days <= 0 {
		return
	}
	n, err := m.st.PurgeRemoved(ctx, m.now().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		m.log.Warn("purge removed jobs", "err", err)
	} else if n > 0 {
		m.log.Info("purged removed jobs", "count", n)
	}
}
