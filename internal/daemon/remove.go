package daemon

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// ErrUndoUnknown means the token was never handed out by this daemon:
// mistyped, or from before a restart (tokens live in memory only).
var ErrUndoUnknown = errors.New("no undo with that token: check that you copied all of it; tokens also end when the daemon restarts. Add the link again with `lazyaria add`; files that were deleted are in the Trash")

// ErrUndoExpired means the token's undo window has passed.
var ErrUndoExpired = errors.New("the undo has expired")

// undoKeep is how long an expired token is still recognised as expired
// rather than unknown.
const undoKeep = 24 * time.Hour

func undoExpired(secs int) error {
	unit := "seconds"
	if secs == 1 {
		unit = "second"
	}
	return fmt.Errorf("%w: undo works for %d %s after a remove (remove.undo_seconds). Add the link again with `lazyaria add`; files that were deleted are in the Trash", ErrUndoExpired, secs, unit)
}

type undoRecord struct {
	until time.Time
	secs  int                      // the window, for the message once it has passed
	prev  map[string]store.State   // job id -> state before removal
	files map[string][]trash.Entry // job id -> its trashed files
	moved map[string][]keptFile    // job id -> its unfinished files moved into Dir
}

// Remove soft-deletes jobs: the engine forgets them, the rows stay as
// removed until purged. The returned token undoes it for remove.undo_seconds.
// kept lists torrent folders left in place with deleteFiles because nothing
// says which of their files are the torrent's.
//
// With deleteFiles the order matters: check a trash exists, stop the
// download so aria2 saves its control file, drop it, mark the row removed,
// and only then trash the files. The trash move runs without m.mu, as it may
// copy across disks for minutes; the removed row keeps every other action,
// the poll and Reconcile away from the job meanwhile. If trashing fails, the
// files are put back and the row too; a job that was running is added
// again, paused.
func (m *Manager) Remove(ctx context.Context, ids []string, deleteFiles bool) (token string, kept []string, err error) {
	ctx = context.WithoutCancel(ctx) // see Add
	prev := map[string]store.State{}
	files := map[string][]trash.Entry{}
	moved := map[string][]keptFile{}
	var moves []*pendingTrash
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
		unmark(j)
		if err := m.markRemoved(ctx, j); err != nil {
			return err
		}
		if deleteFiles {
			moves = append(moves, &pendingTrash{j: j, listed: listed})
			return nil
		}
		ks, err := keepUnfinished(j)
		if err != nil {
			m.log.Warn("move the unfinished download into the download folder; it stays in its staging folder", "job", j.ID, "path", stagingDir(j), "err", err)
		} else if j.Staged && j.Publish == nil {
			if files[j.ID], err = m.clearStaging(j); err != nil {
				m.log.Warn("trash what is left in the staging folder; it stays there", "job", j.ID, "path", stagingDir(j), "err", err)
			}
		}
		m.removed(ctx, j)
		prev[j.ID], moved[j.ID] = j.State, ks
		return nil
	})
	for _, p := range moves {
		p.es, p.err = m.Files.RemoveFiles(ctx, p.j, p.listed)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	errs := []error{err}
	for _, p := range moves {
		j := p.j
		if p.err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", j.ID, errors.Join(p.err, m.unremove(ctx, j))))
			continue
		}
		if j.Staged {
			os.Remove(filepath.Dir(stagingDir(j))) // .laria, once no job uses it
		}
		m.removed(ctx, j)
		prev[j.ID], files[j.ID] = j.State, p.es
		if path, ok := payloadPath(j); ok && p.listed == nil && isBT(j.Kind) && isDir(path) {
			kept = append(kept, path)
		}
	}
	err = errors.Join(errs...)
	secs := m.s.Load().Remove.UndoSeconds
	if secs <= 0 || len(prev) == 0 {
		return "", kept, err
	}
	now := m.now()
	for tok, r := range m.undo {
		if now.After(r.until.Add(undoKeep)) {
			delete(m.undo, tok)
		} else if now.After(r.until) {
			m.undo[tok] = undoRecord{until: r.until, secs: r.secs} // only the expiry is still needed
		}
	}
	tok := rand.Text()
	m.undo[tok] = undoRecord{until: now.Add(time.Duration(secs) * time.Second), secs: secs, prev: prev, files: files, moved: moved}
	return tok, kept, err
}

// pendingTrash is a removed job whose files still go to the trash.
type pendingTrash struct {
	j      *store.Job // as it was before the removal
	listed []string
	es     []trash.Entry
	err    error
}

// markRemoved stores j as removed, with the live byte count for a row
// brought back by Undo.
func (m *Manager) markRemoved(ctx context.Context, j *store.Job) error {
	now := m.now()
	_, err := m.changeAs(ctx, "job.removed", j.ID, "removed", "", func(x *store.Job) {
		x.State, x.RemovedAt, x.RetryAt = store.Removed, &now, nil
		x.DoneBytes = j.DoneBytes
	})
	return err
}

// removed finishes a removal once j's files are dealt with.
func (m *Manager) removed(ctx context.Context, j *store.Job) {
	if seedsFromStaging(j) {
		// Seeding is over; the published copy is in Dir or the trash.
		m.dropStaging(j)
		m.set(ctx, j, "", "", func(x *store.Job) { x.Staged = false })
	}
	m.prog.forget(j.ID)
}

// unremove puts back the row of a job whose files could not be trashed.
// One that was running is added again, paused.
func (m *Manager) unremove(ctx context.Context, j *store.Job) error {
	back, err := m.changeAs(ctx, "job.added", j.ID, "remove-failed", "", func(x *store.Job) {
		x.State, x.RemovedAt, x.RetryAt = j.State, nil, j.RetryAt
	})
	if err != nil {
		return err
	}
	switch {
	case running(j.State) && j.EngineRef != nil:
		cp := *back
		cp.EngineRef = nil
		if _, err := m.readd(ctx, &cp, true, "remove-failed"); err != nil {
			return fmt.Errorf("could not re-add the download: %w", err)
		}
	case j.State == store.RetryWait:
		m.armRetry(back)
	}
	return nil
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
	if !ok {
		return ErrUndoUnknown
	}
	if m.now().After(rec.until) {
		return undoExpired(rec.secs)
	}
	var errs []error
	left := undoRecord{until: rec.until, secs: rec.secs, prev: map[string]store.State{}, files: map[string][]trash.Entry{}, moved: map[string][]keptFile{}}
	for id, was := range rec.prev {
		// Entries an earlier attempt already put back would fail the pre-check.
		todo := inTrash(rec.files[id])
		if err := m.Files.RestoreFiles(ctx, todo); err != nil {
			errs = append(errs, fmt.Errorf("job %s stays removed, its files are still in the trash: %w", id, err))
			left.prev[id], left.files[id], left.moved[id] = was, inTrash(todo), rec.moved[id]
			continue
		}
		if err := putBack(rec.moved[id]); err != nil {
			errs = append(errs, fmt.Errorf("job %s stays removed, its unfinished files are still in %s: %w", id, filepath.Dir(rec.moved[id][0].To), err))
			left.prev[id], left.moved[id] = was, rec.moved[id]
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
