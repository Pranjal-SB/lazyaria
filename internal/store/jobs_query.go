package store

import (
	"context"
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

// PurgeDone hard-deletes finished jobs that finished before the cutoff.
func (s *Store) PurgeDone(ctx context.Context, before time.Time) (int, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE state='done' AND finished_at < ?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, err := r.RowsAffected()
	return int(n), err
}
