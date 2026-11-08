package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// each runs fn for every id under the manager lock and joins the errors.
func (m *Manager) each(ctx context.Context, ids []string, fn func(context.Context, *store.Job) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for _, id := range ids {
		j, err := m.st.GetJob(ctx, id)
		if err == nil {
			err = fn(ctx, j)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) idsIn(ctx context.Context, states ...store.State) ([]string, error) {
	js, err := m.st.ListJobs(ctx, store.Filter{States: states})
	ids := make([]string, len(js))
	for i, j := range js {
		ids[i] = j.ID
	}
	return ids, err
}

// lost reports whether the engine no longer runs ref: unknown, failed or removed.
func (m *Manager) lost(ctx context.Context, ref engine.EngineRef) bool {
	sts, err := m.e.Status(ctx, []engine.EngineRef{ref})
	return err == nil && (!sts[0].Known || sts[0].State == "error" || sts[0].State == "removed")
}

func (m *Manager) Pause(ctx context.Context, ids []string) error { return m.each(ctx, ids, m.pause) }

func (m *Manager) Resume(ctx context.Context, ids []string) error { return m.each(ctx, ids, m.resume) }

func (m *Manager) PauseAll(ctx context.Context) error {
	ids, err := m.idsIn(ctx, store.Queued, store.Active, store.RetryWait)
	if err != nil {
		return err
	}
	return m.Pause(ctx, ids)
}

func (m *Manager) ResumeAll(ctx context.Context) error {
	ids, err := m.idsIn(ctx, store.Paused)
	if err != nil {
		return err
	}
	return m.Resume(ctx, ids)
}

func (m *Manager) pause(ctx context.Context, j *store.Job) error {
	switch j.State {
	case store.Active, store.Queued:
		if j.EngineRef != nil {
			if err := m.e.Pause(ctx, refOf(j)); err != nil && !m.lost(ctx, refOf(j)) {
				return err
			}
		}
	case store.RetryWait:
		m.cancelRetry(j.ID)
	default:
		return nil
	}
	it, seen := m.prog.last(j.ID)
	m.to(ctx, j, store.Paused, "", func(x *store.Job) {
		x.RetryAt = nil
		if seen {
			x.DoneBytes = &it.Done
		}
	})
	return nil
}

// resume continues a paused job, or retries a failed one by adding it again.
func (m *Manager) resume(ctx context.Context, j *store.Job) error {
	switch j.State {
	case store.Paused:
		if j.EngineRef != nil {
			err := m.e.Resume(ctx, refOf(j))
			if err == nil {
				m.to(ctx, j, store.Queued, "", nil)
				return nil
			}
			if !m.lost(ctx, refOf(j)) {
				return err
			}
		}
	case store.Queued:
		if j.EngineRef != nil {
			return nil
		}
	case store.Error, store.RetryWait:
		m.cancelRetry(j.ID)
	default:
		return nil
	}
	_, err := m.readd(ctx, j, false, "resumed")
	return err
}

// RefreshURI points a job at a new link (spec §4.7) and clears its error.
func (m *Manager) RefreshURI(ctx context.Context, id, uri string) (*store.Job, error) {
	if err := checkLink(uri); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.st.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	switch {
	case j.State == store.Done || j.State == store.Seeding:
		return nil, engine.ErrAlreadyComplete
	case j.State == store.Removed:
		return nil, errors.New("the job was removed; add the link again")
	case j.Kind == "torrent" || j.Kind == "magnet":
		return nil, engine.ErrNotSupported
	}
	opts := maps.Clone(j.Options)
	clean := moveAuth(uri, opts)
	newRef := ""
	if j.EngineRef != nil {
		r, err := m.e.ChangeURI(ctx, refOf(j), uri)
		if err != nil && !m.lost(ctx, refOf(j)) {
			return nil, err
		}
		newRef = string(r) // "" when the engine lost it: re-added below
	}
	m.cancelRetry(id)
	stopped := j.State == store.Error || j.State == store.RetryWait
	j, err = m.change(ctx, id, "refreshed", "", func(x *store.Job) {
		x.URIs, x.Options = []string{clean}, opts
		if newRef == "" {
			return
		}
		x.EngineRef = &newRef
		if stopped {
			x.State, x.RetryAt, x.RetryCount = store.Queued, nil, 0
			x.ErrorKind, x.ErrorCode, x.ErrorMsg = nil, nil, nil
		}
	})
	if err != nil || newRef != "" || j.State == store.Paused {
		return j, err
	}
	return m.readd(ctx, j, false, "readded")
}

// checkLink accepts the links a refresh may use: http(s), ftp or sftp,
// parseable, under 8 KiB.
func checkLink(raw string) error {
	u, err := url.Parse(raw)
	if len(raw) >= 8<<10 || err != nil || u.Host == "" {
		return errors.New("the new link is not a valid URL under 8 KiB; paste the full download link")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ftp", "sftp":
		return nil
	}
	return fmt.Errorf("links with scheme %q cannot be used; use http(s), ftp or sftp", u.Scheme)
}

// SetLimit sets a job's speed limits in bytes/s, or the global ones when
// id is empty. 0 means unlimited.
func (m *Manager) SetLimit(ctx context.Context, id string, down, up int64) error {
	if down < 0 || up < 0 {
		return errors.New("speed limits cannot be negative; use 0 for unlimited")
	}
	if id == "" {
		return m.e.SetGlobalLimit(ctx, down, up)
	}
	return m.each(ctx, []string{id}, func(ctx context.Context, j *store.Job) error {
		if j.EngineRef != nil && running(j.State) {
			if err := m.e.SetLimit(ctx, refOf(j), down, up); err != nil && !m.lost(ctx, refOf(j)) {
				return err
			}
		}
		opts := maps.Clone(j.Options)
		for k, v := range map[string]int64{"max-download-limit": down, "max-upload-limit": up} {
			delete(opts, k)
			if v > 0 {
				opts[k] = strconv.FormatInt(v, 10)
			}
		}
		_, err := m.change(ctx, j.ID, "limit", fmt.Sprintf("down=%d up=%d", down, up), func(x *store.Job) {
			x.LimitDown, x.LimitUp, x.Options = down, up, opts
		})
		return err
	})
}

// StopSeeding ends seeding early; the job is done.
func (m *Manager) StopSeeding(ctx context.Context, ids []string) error {
	return m.each(ctx, ids, func(ctx context.Context, j *store.Job) error {
		if j.State != store.Seeding {
			return nil
		}
		if err := m.e.Remove(ctx, refOf(j)); err != nil {
			return err
		}
		now := m.now()
		m.to(ctx, j, store.Done, "seeding stopped", func(x *store.Job) { x.FinishedAt = &now })
		return nil
	})
}
