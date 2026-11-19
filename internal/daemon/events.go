package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// handleEvent reads the download's status once and applies it; the status,
// not the notification, decides the new state.
func (m *Manager) handleEvent(ctx context.Context, ev engine.EngineEvent) {
	if ev.Kind == engine.KindResync {
		if err := m.Reconcile(ctx); err != nil {
			m.log.Warn("reconcile after resync", "err", err)
		}
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobByRef(ctx, ev.Ref)
	if j == nil {
		return
	}
	sts, err := m.e.Status(ctx, []engine.EngineRef{ev.Ref})
	if err != nil {
		m.log.Warn("read status after event", "ref", ev.Ref, "kind", ev.Kind, "err", err)
		return
	}
	if sts[0].Known {
		m.apply(ctx, j, sts[0], ev.Kind)
	}
}

func (m *Manager) jobByRef(ctx context.Context, ref engine.EngineRef) *store.Job {
	j, err := m.st.FindByEngineRef(ctx, string(ref))
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		m.log.Warn("find job by ref", "ref", ref, "err", err)
	}
	return j
}

// apply moves j to whatever st says and returns the job as stored. Only
// jobs with a live download take engine state; errored, waiting and
// finished rows hold refs the engine no longer works on.
func (m *Manager) apply(ctx context.Context, j *store.Job, st engine.EngineStatus, kind string) *store.Job {
	if !running(j.State) {
		return j
	}
	j = m.trackName(ctx, j, st)
	j, paused := m.learnTotal(ctx, j, st)
	if paused {
		return j
	}
	switch {
	case st.State == "complete" && st.FollowedBy != "":
		return m.adopt(ctx, j, st)
	case st.State == "complete":
		now := m.now()
		return m.to(ctx, j, store.Done, "", func(x *store.Job) {
			x.FinishedAt, x.RetryCount = &now, 0
			x.ErrorKind, x.ErrorCode, x.ErrorMsg = nil, nil, nil
			setBytes(x, st)
		})
	case st.State == "error":
		return m.failed(ctx, j, st)
	}
	to := j.State
	// A paused row stays paused: aria2 reports a pause only once it has
	// settled, and only Resume may undo the user's choice.
	switch {
	case j.State == store.Paused:
	case kind == engine.KindBTComplete || (st.Seeder && st.State == "active"):
		to = store.Seeding
	case st.State == "active" && j.State == store.Queued:
		to = store.Active
	case st.State == "waiting" && j.State == store.Active:
		to = store.Queued
	case st.State == "paused" && j.State != store.Seeding:
		to = store.Paused
	}
	if to == j.State {
		return j
	}
	now := m.now()
	return m.to(ctx, j, to, "", func(x *store.Job) {
		if to == store.Active && x.StartedAt == nil {
			x.StartedAt = &now
		}
		setBytes(x, st)
	})
}

func setBytes(x *store.Job, st engine.EngineStatus) {
	setDone(x, st.Done)
	if st.Total > 0 {
		total := st.Total
		x.TotalBytes = &total
	}
}

// setDone records done bytes, but a 0 never lowers a positive count: a
// retry that failed before any reply must not cost the job ownership of
// its file. trackName resets the count when the file changes.
func setDone(x *store.Job, done int64) {
	if done > 0 || x.DoneBytes == nil || *x.DoneBytes <= 0 {
		x.DoneBytes = &done
	}
}

// trackName follows the file aria2 actually writes, which differs from out
// after an auto-rename (spec G7), and learns a torrent's name.
func (m *Manager) trackName(ctx context.Context, j *store.Job, st engine.EngineStatus) *store.Job {
	if st.IsBT {
		if j.Name != "download" || st.Name == "" || st.Name == "download" || strings.HasPrefix(st.Name, "[METADATA]") {
			return j
		}
		name := st.Name
		return m.set(ctx, j, "", "", func(x *store.Job) { x.Name = name })
	}
	if st.Path == "" {
		return j
	}
	rel, err := filepath.Rel(workDir(j), st.Path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || (j.Out != nil && *j.Out == rel) {
		return j
	}
	kind := ""
	if j.Out != nil {
		kind = "renamed"
	}
	name := filepath.Base(rel)
	done := st.Done // the bytes counted so far were for another file
	return m.set(ctx, j, kind, rel, func(x *store.Job) { x.Out, x.Name, x.DoneBytes = &rel, name, &done })
}

// learnTotal stores a newly reported size and, the first time one shows
// up, runs the space check the add could not (spec §8.2).
func (m *Manager) learnTotal(ctx context.Context, j *store.Job, st engine.EngineStatus) (*store.Job, bool) {
	if st.Total <= 0 || (j.TotalBytes != nil && *j.TotalBytes == st.Total) {
		return j, false
	}
	total := st.Total
	first := j.TotalBytes == nil || *j.TotalBytes <= 0
	busy := j.State == store.Active || j.State == store.Queued
	if ns := m.noSpace(j.Dir, total, st.Done); first && busy && ns != nil && st.State != "complete" && st.State != "error" {
		if err := m.e.Pause(ctx, refOf(j)); err != nil {
			m.log.Warn("pause for lack of space", "job", j.ID, "err", err)
		} else {
			j = m.to(ctx, j, store.Paused, "no_space", func(x *store.Job) { x.TotalBytes = &total })
			m.banner("no_space", map[string]any{"job": j.ID, "dir": j.Dir, "need": ns.Need, "free": ns.Free})
			return j, true
		}
	}
	return m.set(ctx, j, "", "", func(x *store.Job) { x.TotalBytes = &total }), false
}

// adopt moves a magnet job onto the torrent its metadata download started
// (spec §4.4), and a link to a .torrent or metalink onto what aria2 started
// from that file. The row points at the child before the parent is dropped,
// so a crash in between leaves at worst a stale finished result.
// shortcut: a metalink with several files starts several downloads and the
// job follows only the first (one engine ref per job); the others run in
// aria2 unseen. Following all needs one job per child.
func (m *Manager) adopt(ctx context.Context, j *store.Job, st engine.EngineStatus) *store.Job {
	parent := refOf(j)
	child := string(st.FollowedBy)
	// A link's Out, name and byte counts were the .torrent or metalink
	// file's. The child's status names the payload on the next read. The
	// link, not the kind, decides: after one adoption the kind is torrent,
	// yet a re-add fetches the .torrent again. A .torrent file is never
	// followed, so only a magnet link is not one.
	link := len(j.URIs) > 0 && jobKind(j.URIs, nil) != "magnet"
	bt := false
	if link {
		if sts, err := m.e.Status(ctx, []engine.EngineRef{st.FollowedBy}); err == nil {
			bt = sts[0].IsBT
		}
	}
	j2, err := m.change(ctx, j.ID, "adopted", child, func(x *store.Job) {
		x.EngineRef = &child
		if link {
			x.Out, x.Name, x.TotalBytes, x.DoneBytes = nil, "download", nil, nil
		}
		if bt {
			x.Kind = "torrent"
		}
	})
	if err != nil {
		m.log.Error("adopt torrent", "job", j.ID, "err", err)
		return j
	}
	if err := m.e.Remove(ctx, parent); err != nil {
		m.log.Warn("drop magnet metadata download", "ref", parent, "err", err)
	}
	return j2
}
