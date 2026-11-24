package daemon

import (
	"context"
	"math"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Actions a job may allow (store.Job.Actions).
const (
	ActPause       = "pause"
	ActResume      = "resume"
	ActRemove      = "remove"
	ActRefresh     = "refresh"
	ActStopSeeding = "stop_seeding"
	ActOpen        = "open"
)

// Summary is the totals a client shows over the whole list.
type Summary struct {
	Active     int    `json:"active"`
	Waiting    int    `json:"waiting"`    // queued, paused, retry-wait
	Continuing int    `json:"continuing"` // active, queued, retry-wait: what goes on with no client
	Attention  int    `json:"attention"`  // error
	Seeding    int    `json:"seeding"`
	Done       int    `json:"done"`
	Down       int64  `json:"speed_down"`
	Up         int64  `json:"speed_up"`
	ETASeconds *int64 `json:"eta_seconds"` // all active downloads; null when any size or speed is unknown
}

// errorLabels name a failed job's error kind (errmap.go) in words.
var errorLabels = map[string]string{
	"link-expired": "link expired",
	"not-found":    "not found",
	"rate-limited": "rate limited",
	"disk-full":    "disk full",
	"auth":         "login needed",
	"network":      "network error",
	"exists":       "file exists",
	"dir-missing":  "folder missing",
}

// Label is a job's state in the words a client shows.
func Label(j *store.Job) string {
	switch j.State {
	case store.Active:
		if j.TotalBytes == nil && (j.DoneBytes == nil || *j.DoneBytes == 0) {
			return "starting"
		}
		return "downloading"
	case store.RetryWait:
		return "retrying"
	case store.Error:
		kind := ""
		if j.ErrorKind != nil {
			kind = *j.ErrorKind
		}
		if l, ok := errorLabels[kind]; ok {
			return l
		}
		if code, ok := strings.CutPrefix(kind, "http-"); ok && code != "" && strings.Trim(code, "0123456789") == "" {
			return "server error " + code
		}
		return "failed"
	}
	return string(j.State)
}

// Actions lists what the Manager does something for on j now. It mirrors
// Manager.pause, resume, StopSeeding and RefreshURI.
func Actions(j *store.Job) []string {
	if j.State == store.Removed {
		return nil
	}
	var a []string
	switch j.State {
	case store.Active, store.Queued, store.RetryWait:
		a = append(a, ActPause)
	}
	switch {
	case j.State == store.Paused, j.State == store.Error, j.State == store.RetryWait,
		j.State == store.Queued && j.EngineRef == nil:
		a = append(a, ActResume)
	}
	if j.State == store.Seeding {
		a = append(a, ActStopSeeding)
	}
	a = append(a, ActRemove)
	if j.State != store.Done && j.State != store.Seeding && j.Kind != "torrent" && j.Kind != "magnet" && !legacy(j) {
		a = append(a, ActRefresh)
	}
	if j.State == store.Done || j.State == store.Seeding {
		a = append(a, ActOpen)
	}
	return a
}

// decorate returns a copy of j with live progress, its label and actions.
func (m *Manager) decorate(j *store.Job) *store.Job {
	if j == nil {
		return nil
	}
	c := *m.prog.overlay(j)
	c.Label, c.Actions = Label(&c), Actions(&c)
	return &c
}

// summary counts the jobs by state and totals the speeds of the latest
// poll. With nothing active or seeding, speeds are 0 and the ETA unknown.
func (m *Manager) summary(ctx context.Context) Summary {
	n, err := m.st.CountByState(ctx)
	if err != nil {
		m.log.Warn("summary: count jobs", "err", err)
	}
	s := Summary{Active: n[store.Active], Waiting: n[store.Queued] + n[store.Paused] + n[store.RetryWait],
		Attention: n[store.Error], Seeding: n[store.Seeding], Done: n[store.Done],
		Continuing: n[store.Active] + n[store.Queued] + n[store.RetryWait]}
	if s.Active == 0 && s.Seeding == 0 {
		return s
	}
	js, err := m.st.ListJobs(ctx, store.Filter{States: []store.State{store.Active, store.Seeding}})
	if err != nil {
		m.log.Warn("summary: list jobs", "err", err)
		return s
	}
	var left, down int64
	known := true
	for _, j := range js {
		it, ok := m.prog.last(j.ID)
		s.Down += it.Down
		s.Up += it.Up
		if j.State != store.Active {
			continue
		}
		known = known && ok && it.Total > 0
		left += it.Total - it.Done
		down += it.Down
	}
	if known && s.Active > 0 && down > 0 {
		eta := int64(math.Round(float64(left) / float64(down)))
		s.ETASeconds = &eta
	}
	return s
}

// lifecycleFrame tells progress subscribers the new totals after a job
// changed, so a client's top bar never waits for the next poll.
func (m *Manager) lifecycleFrame(ctx context.Context) {
	if m.observers.Load() > 0 {
		m.bus.PublishProgress(Frame{Summary: m.summary(ctx)})
	}
}
