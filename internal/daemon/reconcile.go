package daemon

import (
	"context"
	"fmt"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Reconcile brings the database and the engine back in line after a start,
// an engine restart or lost notifications (spec §4.4).
func (m *Manager) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list, err := m.e.List(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}
	known := make(map[string]engine.EngineStatus, len(list))
	for _, s := range list {
		known[string(s.Ref)] = s
	}
	js, err := m.st.ListJobs(ctx, store.Filter{States: []store.State{store.Queued, store.Active, store.Paused, store.RetryWait, store.Seeding}})
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}
	for _, j := range js {
		if j.State == store.RetryWait {
			m.armRetry(j)
			continue
		}
		if j.EngineRef != nil {
			st, ok := known[*j.EngineRef]
			if !ok {
				// List pages through buckets a download can move between,
				// so a miss is confirmed before anything is added twice.
				sts, err := m.e.Status(ctx, []engine.EngineRef{refOf(j)})
				if err != nil {
					m.log.Warn("reconcile: confirm missing download", "job", j.ID, "err", err)
					continue
				}
				st = sts[0]
			}
			if st.Known {
				m.apply(ctx, j, st, "")
				continue
			}
		}
		m.recover(ctx, j)
	}
	return nil
}

// recover re-adds a job the engine does not have.
func (m *Manager) recover(ctx context.Context, j *store.Job) {
	if j.State == store.Seeding {
		// The payload is complete; seeding does not outlive the engine.
		now := m.now()
		m.to(ctx, j, store.Done, "seeding ended with the engine", func(x *store.Job) { x.FinishedAt = &now })
		return
	}
	if m.dirMissing(j) {
		kind := "dir-missing"
		msg := fmt.Sprintf("%s is missing or on a different drive; mount it and retry", j.Dir)
		m.to(ctx, j, store.Error, kind, func(x *store.Job) { x.ErrorKind, x.ErrorMsg = &kind, &msg })
		return
	}
	if _, err := m.readd(ctx, j, j.State == store.Paused, "readded"); err != nil {
		m.log.Warn("reconcile: re-add", "job", j.ID, "err", err)
	}
}

// dirMissing is the unmounted-drive guard (spec G6): the directory is gone
// or now lives on another device, so writing there would fill the wrong disk.
func (m *Manager) dirMissing(j *store.Job) bool {
	if !m.s.Reliability.UnmountedGuard {
		return false
	}
	d, err := m.dev(j.Dir)
	return err != nil || (j.DirDev != nil && d != *j.DirDev)
}

// readd hands j to the engine again from its stored row, dropping the old
// download's result first. why is recorded in the job's events; anything
// but an automatic retry also resets the retry count.
func (m *Manager) readd(ctx context.Context, j *store.Job, paused bool, why string) (*store.Job, error) {
	if j.EngineRef != nil {
		if err := m.e.Remove(ctx, refOf(j)); err != nil {
			m.log.Warn("drop old download", "job", j.ID, "err", err)
		}
	}
	ref, err := m.e.Add(ctx, m.request(j, paused))
	if err != nil {
		return j, err
	}
	state := store.Queued
	if paused {
		state = store.Paused
	}
	return m.change(ctx, j.ID, why, string(ref), func(x *store.Job) {
		r := string(ref)
		x.EngineRef, x.State, x.RetryAt = &r, state, nil
		x.ErrorKind, x.ErrorCode, x.ErrorMsg = nil, nil, nil
		if why != "retry" {
			x.RetryCount = 0
		}
	})
}
