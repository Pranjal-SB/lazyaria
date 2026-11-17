package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func (s *Store) list(ctx context.Context, where string, a []any, limit, offset int) ([]*Job, error) {
	q := `SELECT ` + cols + ` FROM jobs ` + where + ` ` + smartOrder
	if limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		a = append(a, limit, offset)
	}
	rows, err := s.db.QueryContext(ctx, q, a...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) ListJobs(ctx context.Context, f Filter) ([]*Job, error) {
	var conds []string
	var a []any
	if len(f.States) > 0 {
		conds = append(conds, `state IN (`+strings.TrimSuffix(strings.Repeat("?,", len(f.States)), ",")+`)`)
		for _, st := range f.States {
			a = append(a, string(st))
		}
	} else if !f.IncludeHistory {
		conds = append(conds, `state NOT IN ('done','removed')`)
	} else {
		conds = append(conds, `state != 'removed'`)
	}
	if len(f.IDs) > 0 {
		// One JSON parameter: SQLite caps the number of ? at 32766.
		ids, err := json.Marshal(f.IDs)
		if err != nil {
			return nil, err
		}
		conds = append(conds, `id IN (SELECT value FROM json_each(?))`)
		a = append(a, string(ids))
	}
	return s.list(ctx, `WHERE `+strings.Join(conds, " AND "), a, f.Limit, f.Offset)
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Search matches q against names and URIs, skipping removed jobs.
func (s *Store) Search(ctx context.Context, q string, limit, offset int) ([]*Job, error) {
	pat := `%` + likeEscaper.Replace(q) + `%`
	return s.list(ctx, `WHERE state != 'removed' AND (name LIKE ? ESCAPE '\' OR
		EXISTS (SELECT 1 FROM json_each(jobs.uris) WHERE value LIKE ? ESCAPE '\'))`,
		[]any{pat, pat}, limit, offset)
}

// FindByNormalizedURL returns a live job that has norm among its normalized URIs.
func (s *Store) FindByNormalizedURL(ctx context.Context, norm string) (*Job, error) {
	js, err := s.list(ctx, `WHERE state != 'removed' AND EXISTS
		(SELECT 1 FROM json_each(jobs.norm_uris) WHERE value = ?)`, []any{norm}, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(js) == 0 {
		return nil, ErrNotFound
	}
	return js[0], nil
}

// PurgeRemoved hard-deletes soft-removed jobs removed before the cutoff.
func (s *Store) PurgeRemoved(ctx context.Context, before time.Time) (int, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE state='removed' AND removed_at < ?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, err := r.RowsAffected()
	return int(n), err
}

// DeleteJob removes a job row and its events, for an add undone before it
// reached the engine.
func (s *Store) DeleteJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id=?`, id)
	return err
}

// PurgeDone hard-deletes finished jobs that finished before the cutoff.
func (s *Store) PurgeDone(ctx context.Context, before time.Time) (int, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE state='done' AND finished_at < ?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, err := r.RowsAffected()
	return int(n), err
}

// CountJobs counts the jobs in any of states. With no states it counts nothing and returns 0.
func (s *Store) CountJobs(ctx context.Context, states ...State) (int, error) {
	if len(states) == 0 {
		return 0, nil
	}
	a := make([]any, len(states))
	for i, st := range states {
		a[i] = string(st)
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state IN (`+strings.TrimSuffix(strings.Repeat("?,", len(states)), ",")+`)`, a...).Scan(&n)
	return n, err
}
