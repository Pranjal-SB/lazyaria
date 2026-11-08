package daemon

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Backoff spaces automatic retries: Base, then Base*Factor, and so on, for
// at most Max attempts. Jitter spreads each delay by ±Jitter.
type Backoff struct {
	Base   time.Duration
	Factor float64
	Max    int
	Jitter float64
}

// Delay is the wait before attempt (1-based). rnd returns values in [0,1).
func (b Backoff) Delay(attempt int, rnd func() float64) time.Duration {
	d := float64(b.Base) * math.Pow(b.Factor, float64(attempt-1))
	if b.Jitter > 0 && rnd != nil {
		d *= 1 + b.Jitter*(2*rnd()-1)
	}
	return time.Duration(d)
}

func (m *Manager) backoff() Backoff {
	base, err := config.ParseDuration(m.s.Retry.BackoffBase)
	if err != nil {
		base = 30 * time.Second
	}
	return Backoff{Base: base, Factor: m.s.Retry.BackoffFactor, Max: m.s.Retry.BackoffMaxAttempts, Jitter: 0.2}
}

// retryLater parks j in retry-wait with a persisted retry_at, or gives up
// with kind once the attempts are used up.
func (m *Manager) retryLater(ctx context.Context, j *store.Job, st engine.EngineStatus, kind string) *store.Job {
	b := m.backoff()
	attempt := j.RetryCount + 1
	if attempt > b.Max {
		return m.to(ctx, j, store.Error, kind, func(x *store.Job) { setErr(x, kind, st) })
	}
	at := m.now().Add(b.Delay(attempt, m.rnd))
	j = m.to(ctx, j, store.RetryWait, fmt.Sprintf("%s, attempt %d at %s", kind, attempt, at.Format(time.RFC3339)), func(x *store.Job) {
		setErr(x, kind, st)
		x.RetryCount, x.RetryAt = attempt, &at
	})
	if j.State == store.RetryWait {
		m.armRetry(j)
	}
	return j
}

// armRetry starts j's timer for its retry_at; a time in the past fires at
// once. The caller holds m.mu.
func (m *Manager) armRetry(j *store.Job) {
	m.cancelRetry(j.ID)
	var d time.Duration
	if j.RetryAt != nil {
		d = max(j.RetryAt.Sub(m.now()), 0)
	}
	id := j.ID
	var t *time.Timer
	t = time.AfterFunc(d, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.retries[id] == t { // not cancelled or replaced meanwhile
			m.retryNow(id)
		}
	})
	m.retries[id] = t
}

func (m *Manager) cancelRetry(id string) {
	if t := m.retries[id]; t != nil {
		t.Stop()
		delete(m.retries, id)
	}
}

func (m *Manager) stopRetries() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.retries {
		m.cancelRetry(id)
	}
}

// fireRetry runs a due retry now.
func (m *Manager) fireRetry(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retryNow(id)
}

func (m *Manager) retryNow(id string) {
	delete(m.retries, id)
	ctx := m.ctx
	if ctx.Err() != nil {
		return
	}
	j, err := m.st.GetJob(ctx, id)
	if err != nil || j.State != store.RetryWait {
		return // paused, removed or refreshed while waiting
	}
	_, err = m.readd(ctx, j, false, "retry")
	if err == nil || errors.Is(err, engine.ErrNotRunning) {
		return // without an engine, Reconcile re-arms the timer once it is back
	}
	kind, msg := "add-failed", err.Error()
	m.to(ctx, j, store.Error, kind, func(x *store.Job) { x.ErrorKind, x.ErrorMsg, x.RetryAt = &kind, &msg, nil })
}
