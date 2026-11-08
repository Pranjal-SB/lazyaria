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

// ErrUndoExpired means the undo window has passed or the token is unknown.
var ErrUndoExpired = errors.New("nothing to undo: the undo window has passed")

type undoRecord struct {
	until time.Time
	prev  map[string]store.State   // job id -> state before removal
	files map[string][]trash.Entry // job id -> its trashed files
}

// Remove soft-deletes jobs: the engine forgets them, the rows stay as
// removed until purged. The returned token undoes it for remove.undo_seconds.
func (m *Manager) Remove(ctx context.Context, ids []string, deleteFiles bool) (string, error) {
	prev := map[string]store.State{}
	files := map[string][]trash.Entry{}
	err := m.each(ctx, ids, func(ctx context.Context, j *store.Job) error {
		if j.State == store.Removed {
			return nil
		}
		if j.EngineRef != nil {
			// Without an engine there is nothing to stop; a fresh one starts empty.
			if err := m.e.Remove(ctx, refOf(j)); err != nil && !errors.Is(err, engine.ErrNotRunning) {
				return err
			}
		}
		m.cancelRetry(j.ID)
		var es []trash.Entry
		if deleteFiles {
			// Trash before touching the row: with no trash the remove does not happen.
			var err error
			if es, err = m.Files.RemoveFiles(ctx, j); err != nil {
				return err
			}
		}
		now := m.now()
		_, err := m.changeAs(ctx, "job.removed", j.ID, "removed", "", func(x *store.Job) {
			x.State, x.RemovedAt, x.RetryAt = store.Removed, &now, nil
		})
		if err != nil {
			if rerr := m.Files.RestoreFiles(ctx, es); rerr != nil {
				err = errors.Join(err, rerr)
			}
			return err
		}
		m.prog.forget(j.ID)
		prev[j.ID], files[j.ID] = j.State, es
		return nil
	})
	secs := m.s.Remove.UndoSeconds
	if secs <= 0 || len(prev) == 0 {
		return "", err
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
	return tok, err
}

// Undo restores the rows of a recent Remove. Jobs that were running come
// back paused, and the engine gets them again on Resume.
func (m *Manager) Undo(ctx context.Context, token string) error {
	ctx = context.WithoutCancel(ctx) // see Add
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.undo[token]
	delete(m.undo, token)
	if !ok || m.now().After(rec.until) {
		return ErrUndoExpired
	}
	var errs []error
	for id, es := range rec.files {
		if err := m.Files.RestoreFiles(ctx, es); err != nil {
			errs = append(errs, fmt.Errorf("restore files of job %s: %w", id, err))
		}
	}
	for id, was := range rec.prev {
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
	return errors.Join(errs...)
}
