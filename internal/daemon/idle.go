package daemon

import "time"

// Idle decides when an unused daemon should exit. It is a plain state
// machine; the caller owns the timer and the clock is injectable.
type Idle struct {
	after time.Duration
	now   func() time.Time
	since *time.Time // deadline while idle, nil otherwise
}

func NewIdle(after time.Duration, now func() time.Time) *Idle {
	if now == nil {
		now = time.Now
	}
	return &Idle{after: after, now: now}
}

// Update returns the exit deadline while nothing is active, no client is
// attached and always-on is off. The deadline is fixed until activity resets it.
func (i *Idle) Update(activeJobs, clients int, always bool) (exitAt *time.Time) {
	if activeJobs > 0 || clients > 0 || always {
		i.since = nil
		return nil
	}
	if i.since == nil {
		d := i.now().Add(i.after)
		i.since = &d
	}
	d := *i.since
	return &d
}
