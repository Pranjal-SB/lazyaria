package store

import (
	"context"
	"time"
)

// Event is one entry in a job's audit trail.
type Event struct {
	JobID  string
	At     time.Time
	Kind   string
	Detail string
}

func (s *Store) AddEvent(ctx context.Context, jobID, kind, detail string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO job_events(job_id,at,kind,detail) VALUES(?,?,?,?)`,
		jobID, time.Now().UnixMilli(), kind, detail)
	return err
}

// Events returns a job's events, oldest first.
func (s *Store) Events(ctx context.Context, jobID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id, at, kind, COALESCE(detail,'')
		FROM job_events WHERE job_id=? ORDER BY at, rowid`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var at int64
		if err := rows.Scan(&e.JobID, &at, &e.Kind, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
