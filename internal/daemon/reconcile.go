package daemon

import (
	"context"
	"fmt"
	"path/filepath"

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
	var down error
	left := 0
	for _, j := range js {
		if j.State == store.RetryWait {
			m.armRetry(j)
			continue
		}
		if down != nil {
			left++
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
					if engineDown(err) {
						down, left = err, left+1
					}
					continue
				}
				st = sts[0]
			}
			if !lostStatus(st) {
				m.apply(ctx, j, st, "")
				continue
			}
		}
		if err := m.recover(ctx, j); engineDown(err) {
			down, left = err, left+1
		}
	}
	if down != nil {
		return fmt.Errorf("reconcile: engine unavailable, %d jobs left for the next pass: %w", left, down)
	}
	return nil
}

// lostStatus means the engine no longer has the download, or holds only its
// removed result; either way it must be added again.
func lostStatus(st engine.EngineStatus) bool { return !st.Known || st.State == "removed" }

// recover re-adds a job the engine lost.
func (m *Manager) recover(ctx context.Context, j *store.Job) error {
	if j.State == store.Seeding {
		// The payload is complete; seeding does not outlive the engine.
		now := m.now()
		m.to(ctx, j, store.Done, "seeding ended with the engine", func(x *store.Job) { x.FinishedAt = &now })
		return nil
	}
	if m.dirMissing(j) {
		kind := "dir-missing"
		msg := fmt.Sprintf("%s is missing or on a different drive; mount it and retry", j.Dir)
		m.to(ctx, j, store.Error, kind, func(x *store.Job) { x.ErrorKind, x.ErrorMsg = &kind, &msg })
		return nil
	}
	_, err := m.readd(ctx, j, j.State == store.Paused, "readded")
	if err != nil {
		m.log.Warn("re-add lost download", "job", j.ID, "err", err)
	}
	return err
}

// dirMissing is the unmounted-drive guard (spec G6). st_dev is not stable
// across reboots on btrfs, overlay or NFS, so a changed device counts only
// when the job had started and neither its file nor its control file is
// there any more.
func (m *Manager) dirMissing(j *store.Job) bool {
	if !m.s.Reliability.UnmountedGuard {
		return false
	}
	d, err := m.dev(j.Dir)
	if err != nil {
		return true
	}
	if j.DirDev == nil || d == *j.DirDev || j.StartedAt == nil {
		return false
	}
	name := j.Name
	if j.Out != nil {
		name = *j.Out
	}
	p := filepath.Join(j.Dir, name)
	return !exists(p) && !exists(p+".aria2")
}

// readd hands j to the engine again from its stored row, dropping the old
// download's result first. why is recorded in the job's events; anything
// but an automatic retry also resets the retry count.
func (m *Manager) readd(ctx context.Context, j *store.Job, paused bool, why string) (*store.Job, error) {
	ctx = context.WithoutCancel(ctx) // see Add
	req := m.request(j, paused)
	if j.EngineRef != nil {
		if err := m.e.Remove(ctx, refOf(j)); err != nil {
			m.log.Warn("drop old download", "job", j.ID, "err", err)
		}
	} else {
		req.GID = newGID()
	}
	// A started download whose control file is gone (a server without
	// ranges, or an unknown size) restarts in place instead of beside it.
	req.AllowOverwrite = j.StartedAt != nil && j.Out != nil
	ref, err := m.e.Add(ctx, req)
	if err != nil {
		return j, err
	}
	dev, derr := m.dev(j.Dir)
	state := store.Queued
	if paused {
		state = store.Paused
	}
	return m.change(ctx, j.ID, why, string(ref), func(x *store.Job) {
		r := string(ref)
		x.EngineRef, x.State, x.RetryAt = &r, state, nil
		if derr == nil {
			x.DirDev = &dev
		}
		x.ErrorKind, x.ErrorCode, x.ErrorMsg = nil, nil, nil
		if why != "retry" {
			x.RetryCount = 0
		}
	})
}
