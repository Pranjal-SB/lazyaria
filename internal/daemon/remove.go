package daemon

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// ErrUndoExpired means the undo window has passed or the token is unknown
// (mistyped, or from before a daemon restart: tokens live in memory only).
var ErrUndoExpired = errors.New("nothing to undo: the undo token is unknown or has expired (tokens end after remove.undo_seconds or when the daemon restarts). Add the link again with `lazyaria add`; files that were deleted are in the Trash")

type undoRecord struct {
	until time.Time
	prev  map[string]store.State   // job id -> state before removal
	files map[string][]trash.Entry // job id -> its trashed files
}

// Remove soft-deletes jobs: the engine forgets them, the rows stay as
// removed until purged. The returned token undoes it for remove.undo_seconds.
// kept lists torrent folders left in place with deleteFiles because nothing
// says which of their files are the torrent's.
//
// With deleteFiles the order matters (spec §8.3): check a trash exists, stop
// the download so aria2 saves its control file, drop it, trash the files,
// and only then mark the row removed. If trashing fails after the engine let
// go, the files are put back and the job is added again, paused.
func (m *Manager) Remove(ctx context.Context, ids []string, deleteFiles bool) (token string, kept []string, err error) {
	prev := map[string]store.State{}
	files := map[string][]trash.Entry{}
	err = m.each(ctx, ids, func(ctx context.Context, j *store.Job) error {
		if j.State == store.Removed {
			return nil
		}
		if publishing(j) && !m.copying[j.ID] {
			// Finish the move first, so no published file is left that the
			// row does not account for. If it fails, targets still covers
			// the entries already moved.
			if j2, err := m.publish(ctx, j); err == nil {
				j = j2
			}
		}
		if m.copying[j.ID] {
			return fmt.Errorf("the finished download is being copied into %s; remove it again once that is done", j.Dir)
		}
		var listed []string
		if deleteFiles {
			listed = m.downloadFiles(ctx, j)
			if err := m.Files.Check(ctx, j, listed); err != nil {
				return err
			}
		}
		if j.EngineRef != nil {
			if deleteFiles && (j.State == store.Active || j.State == store.Queued || j.State == store.Seeding) {
				// Pausing first makes aria2 write its control file, so an
				// Undo resumes. The row lags behind polling: the live file
				// name and byte count come from aria2, or the last poll.
				st, ok := m.pauseAndWait(ctx, j)
				if !ok {
					st, ok = m.prog.lastStatus(j.ID)
				}
				if ok {
					j = m.trackName(ctx, j, st)
					setDone(j, st.Done)
				}
			}
			// Without an engine there is nothing to stop; a fresh one starts empty.
			if err := m.e.Remove(ctx, refOf(j)); err != nil && !errors.Is(err, engine.ErrNotRunning) {
				return err
			}
		}
		if j.Staged {
			// A metalink's other files write into the same staging folder.
			refs, _ := m.stagingRefs(ctx, j)
			for _, r := range refs {
				if err := m.e.Remove(ctx, r); err != nil && !errors.Is(err, engine.ErrNotRunning) {
					return err
				}
			}
		}
		m.cancelRetry(j.ID)
		var es []trash.Entry
		if deleteFiles {
			var err error
			if es, err = m.Files.RemoveFiles(ctx, j, listed); err != nil {
				if running(j.State) && j.EngineRef != nil {
					cp := *j
					cp.EngineRef = nil
					if _, rerr := m.readd(ctx, &cp, true, "remove-failed"); rerr != nil {
						err = errors.Join(err, fmt.Errorf("could not re-add the download: %w", rerr))
					}
				}
				return err
			}
		}
		now := m.now()
		_, err := m.changeAs(ctx, "job.removed", j.ID, "removed", "", func(x *store.Job) {
			x.State, x.RemovedAt, x.RetryAt = store.Removed, &now, nil
			x.DoneBytes = j.DoneBytes // the live count, for a row brought back by Undo
		})
		if err != nil {
			if rerr := m.Files.RestoreFiles(ctx, es); rerr != nil {
				err = errors.Join(err, rerr)
			}
			return err
		}
		m.prog.forget(j.ID)
		prev[j.ID], files[j.ID] = j.State, es
		if p, ok := payloadPath(j); deleteFiles && ok && listed == nil && isBT(j.Kind) && isDir(p) {
			kept = append(kept, p)
		}
		return nil
	})
	secs := m.s.Load().Remove.UndoSeconds
	if secs <= 0 || len(prev) == 0 {
		return "", kept, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for tok, r := range m.undo {
		if now.After(r.until) {
			delete(m.undo, tok)
		}
	}
	tok := rand.Text()
	m.undo[tok] = undoRecord{until: now.Add(time.Duration(secs) * time.Second), prev: prev, files: files}
	return tok, kept, err
}

// pauseAndWait pauses a live download and waits (briefly) until aria2 has
// settled, which is when it has written its control file. It returns the
// last status aria2 reported, when it reported one. Failures are only
// logged: the engine removal that follows stops the download anyway.
func (m *Manager) pauseAndWait(ctx context.Context, j *store.Job) (last engine.EngineStatus, ok bool) {
	ref := refOf(j)
	if err := m.e.Pause(ctx, ref); err != nil {
		m.log.Warn("pause before remove", "job", j.ID, "err", err)
		return last, false
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		sts, err := m.e.Status(ctx, []engine.EngineRef{ref})
		if err != nil || !sts[0].Known {
			return last, ok
		}
		last, ok = sts[0], true
		if last.State != "active" {
			return last, ok
		}
	}
	return last, ok
}

// Undo restores the rows of a recent Remove, and their files. Jobs that
// were running come back paused, and the engine gets them again on Resume.
// A job whose files cannot be restored stays removed, with its files in the
// trash, and is reported in the error; the token then stays valid for a retry.
func (m *Manager) Undo(ctx context.Context, token string) error {
	ctx = context.WithoutCancel(ctx) // see Add
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.undo[token]
	if !ok || m.now().After(rec.until) {
		delete(m.undo, token)
		return ErrUndoExpired
	}
	var errs []error
	left := undoRecord{until: rec.until, prev: map[string]store.State{}, files: map[string][]trash.Entry{}}
	for id, was := range rec.prev {
		// Entries an earlier attempt already put back would fail the pre-check.
		todo := inTrash(rec.files[id])
		if err := m.Files.RestoreFiles(ctx, todo); err != nil {
			errs = append(errs, fmt.Errorf("job %s stays removed, its files are still in the trash: %w", id, err))
			left.prev[id], left.files[id] = was, inTrash(todo)
			continue
		}
		to := was
		switch was {
		case store.Queued, store.Active, store.Paused, store.RetryWait:
			to = store.Paused
		case store.Seeding:
			to = store.Done
		}
		_, err := m.changeAs(ctx, "job.added", id, "restored", string(to), func(x *store.Job) {
			x.State, x.RemovedAt = to, nil
			if to == store.Paused {
				x.EngineRef = nil
			}
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("restore job %s: %w", id, err))
		}
	}
	if len(left.prev) == 0 {
		delete(m.undo, token)
	} else {
		m.undo[token] = left
	}
	return errors.Join(errs...)
}

// inTrash keeps the entries whose trashed copy is still there.
func inTrash(es []trash.Entry) []trash.Entry {
	var out []trash.Entry
	for _, e := range es {
		if lexists(e.TrashedPath) {
			out = append(out, e)
		}
	}
	return out
}
