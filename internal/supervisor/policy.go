package supervisor

import "time"

// A run longer than this counts as healthy, so the next crash starts the
// backoff from 1 s again.
const stableRun = time.Minute

// Policy decides how long to wait before respawning aria2c and when to give
// up. The zero counters are ready to use.
type Policy struct {
	MaxBackoff time.Duration
	LoopExits  int           // this many fast exits in a row is a crash loop
	LoopWindow time.Duration // an exit sooner than this after start is fast

	attempt int // exits since the last stable run
	fast    int // fast exits in a row
}

// OnExit records one exit and returns the delay before the next start, or
// giveUp when aria2c is crash looping.
func (p *Policy) OnExit(startedAt, exitedAt time.Time) (delay time.Duration, giveUp bool) {
	up := exitedAt.Sub(startedAt)
	if up >= stableRun {
		p.attempt = 0
	}
	if up < p.LoopWindow {
		p.fast++
	} else {
		p.fast = 0
	}
	if p.fast >= p.LoopExits {
		return 0, true
	}
	delay = min(time.Second<<min(p.attempt, 30), p.MaxBackoff)
	p.attempt++
	return delay, false
}
