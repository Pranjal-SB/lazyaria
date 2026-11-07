package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type State string

const (
	Queued    State = "queued"
	Active    State = "active"
	Paused    State = "paused"
	RetryWait State = "retry-wait"
	Error     State = "error"
	Seeding   State = "seeding"
	Done      State = "done"
	Removed   State = "removed"
)

type Job struct {
	ID, Kind, Engine string
	EngineRef        *string
	State            State
	ErrorKind        *string
	ErrorCode        *int
	ErrorMsg         *string
	Name             string
	URIs             []string
	Torrent          []byte
	Dir              string
	Out              *string
	DirDev           *uint64
	TotalBytes       *int64
	DoneBytes        *int64
	Options          map[string]string
	LimitDown        int64
	LimitUp          int64
	Category         *string
	Queue            string
	Priority         int
	RetryCount       int
	RetryAt          *time.Time
	Referrer         *string
	CreatedAt        time.Time
	StartedAt        *time.Time
	FinishedAt       *time.Time
	RemovedAt        *time.Time
}

// Filter narrows ListJobs. Without IncludeHistory, done and removed jobs are hidden.
type Filter struct {
	States         []State
	IncludeHistory bool
	Limit, Offset  int
}

const cols = `id, kind, engine, engine_ref, state, error_kind, error_code, error_msg, name, uris,
	torrent, dir, out, dir_dev, total_bytes, done_bytes, options, limit_down, limit_up, category,
	queue, priority, retry_count, retry_at, referrer, created_at, started_at, finished_at, removed_at`

// smartOrder: needs-attention, active, waiting, then finished newest first.
const smartOrder = `ORDER BY CASE state WHEN 'error' THEN 0 WHEN 'active' THEN 1
	WHEN 'queued' THEN 2 WHEN 'paused' THEN 2 WHEN 'retry-wait' THEN 2 ELSE 3 END,
	CASE WHEN state IN ('seeding','done') THEN finished_at END DESC, created_at, id`

type scanner interface{ Scan(...any) error }

func ms(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func tm(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.UnixMilli(n.Int64)
	return &t
}

func scanJob(r scanner) (*Job, error) {
	var j Job
	var uris, opts string
	var errCode, dev, total, done, retryAt, created, started, finished, removed sql.NullInt64
	var state string
	err := r.Scan(&j.ID, &j.Kind, &j.Engine, &j.EngineRef, &state, &j.ErrorKind, &errCode, &j.ErrorMsg,
		&j.Name, &uris, &j.Torrent, &j.Dir, &j.Out, &dev, &total, &done, &opts, &j.LimitDown, &j.LimitUp,
		&j.Category, &j.Queue, &j.Priority, &j.RetryCount, &retryAt, &j.Referrer, &created, &started,
		&finished, &removed)
	if err != nil {
		return nil, err
	}
	j.State = State(state)
	if err := json.Unmarshal([]byte(uris), &j.URIs); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(opts), &j.Options); err != nil {
		return nil, err
	}
	if errCode.Valid {
		c := int(errCode.Int64)
		j.ErrorCode = &c
	}
	if dev.Valid {
		d := uint64(dev.Int64) // st_dev stored bit-for-bit in a signed column
		j.DirDev = &d
	}
	if total.Valid {
		j.TotalBytes = &total.Int64
	}
	if done.Valid {
		j.DoneBytes = &done.Int64
	}
	j.RetryAt, j.StartedAt, j.FinishedAt, j.RemovedAt = tm(retryAt), tm(started), tm(finished), tm(removed)
	j.CreatedAt = *tm(created)
	return &j, nil
}

func args(j *Job) ([]any, error) {
	uris, err := json.Marshal(nonNil(j.URIs))
	if err != nil {
		return nil, err
	}
	o := j.Options
	if o == nil {
		o = map[string]string{}
	}
	opts, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	var code, dev any
	if j.ErrorCode != nil {
		code = *j.ErrorCode
	}
	if j.DirDev != nil {
		dev = int64(*j.DirDev)
	}
	return []any{j.ID, j.Kind, j.Engine, j.EngineRef, string(j.State), j.ErrorKind, code, j.ErrorMsg,
		j.Name, string(uris), j.Torrent, j.Dir, j.Out, dev, j.TotalBytes, j.DoneBytes, string(opts),
		j.LimitDown, j.LimitUp, j.Category, j.Queue, j.Priority, j.RetryCount, ms(j.RetryAt), j.Referrer,
		j.CreatedAt.UnixMilli(), ms(j.StartedAt), ms(j.FinishedAt), ms(j.RemovedAt)}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

const placeholders = `?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?`

func (s *Store) InsertJob(ctx context.Context, j *Job) error {
	a, err := args(j)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO jobs(`+cols+`) VALUES(`+placeholders+`)`, a...)
	return err
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getJob(ctx context.Context, q queryer, id string) (*Job, error) {
	j, err := scanJob(q.QueryRowContext(ctx, `SELECT `+cols+` FROM jobs WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return j, err
}

func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) { return getJob(ctx, s.db, id) }

// UpdateJob reads the job, applies fn and writes it back in one transaction.
// fn must not call back into the Store.
func (s *Store) UpdateJob(ctx context.Context, id string, fn func(*Job) error) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	j, err := getJob(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := fn(j); err != nil {
		return nil, err
	}
	j.ID = id
	a, err := args(j)
	if err != nil {
		return nil, err
	}
	set := strings.Join(strings.Fields(strings.ReplaceAll(cols, ",", "=?,")+"=?"), " ")
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET `+set+` WHERE id=?`, append(a, id)...); err != nil {
		return nil, err
	}
	return j, tx.Commit()
}

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
	return s.list(ctx, `WHERE state != 'removed' AND (name LIKE ? ESCAPE '\' OR uris LIKE ? ESCAPE '\')`,
		[]any{pat, pat}, limit, offset)
}

// FindByNormalizedURL returns a live job that has norm as one of its URIs.
func (s *Store) FindByNormalizedURL(ctx context.Context, norm string) (*Job, error) {
	js, err := s.list(ctx, `WHERE state != 'removed' AND EXISTS
		(SELECT 1 FROM json_each(jobs.uris) WHERE value = ?)`, []any{norm}, 1, 0)
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
