package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/textutil"
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
	uris, err := marshal(nonNil(j.URIs))
	if err != nil {
		return nil, err
	}
	norm := make([]string, 0, len(j.URIs))
	for _, u := range j.URIs {
		n, err := textutil.NormalizeURL(u)
		if err != nil {
			n = u
		}
		norm = append(norm, n)
	}
	normJSON, err := marshal(norm)
	if err != nil {
		return nil, err
	}
	o := j.Options
	if o == nil {
		o = map[string]string{}
	}
	opts, err := marshal(o)
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
		j.Name, uris, j.Torrent, j.Dir, j.Out, dev, j.TotalBytes, j.DoneBytes, opts,
		j.LimitDown, j.LimitUp, j.Category, j.Queue, j.Priority, j.RetryCount, ms(j.RetryAt), j.Referrer,
		j.CreatedAt.UnixMilli(), ms(j.StartedAt), ms(j.FinishedAt), ms(j.RemovedAt), normJSON}, nil
}

// marshal skips HTML escaping so & < > stay literal in stored JSON.
func marshal(v any) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

const placeholders = `?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?`

func (s *Store) InsertJob(ctx context.Context, j *Job) error {
	a, err := args(j)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO jobs(`+cols+`, norm_uris) VALUES(`+placeholders+`)`, a...)
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
	set := strings.Join(strings.Fields(strings.ReplaceAll(cols+", norm_uris", ",", "=?,")+"=?"), " ")
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET `+set+` WHERE id=?`, append(a, id)...); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return j, nil
}
