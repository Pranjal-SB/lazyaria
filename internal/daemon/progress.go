package daemon

import (
	"math"
	"sync"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

const (
	speedHalfLife = 5 * time.Second  // speeds are smoothed so they do not jump about
	speedSpan     = 60 * time.Second // the speed history clients graph
)

// SpeedSample is the total transfer rate at one moment.
type SpeedSample struct {
	At   time.Time `json:"at"`
	Down int64     `json:"speed_down"`
	Up   int64     `json:"speed_up"`
}

type jobSpeed struct {
	ema  float64
	at   time.Time
	item ProgressItem
	st   engine.EngineStatus // the sample item came from
}

// progress smooths per-job speeds and keeps the recent total speed history.
type progress struct {
	mu   sync.Mutex
	jobs map[string]*jobSpeed
	ring []SpeedSample
	sent time.Time // last progress push
}

func newProgress() *progress { return &progress{jobs: map[string]*jobSpeed{}} }

// observe feeds one status sample and returns the job's progress, with Down
// as the EMA speed and ETA derived from it (nil when unknown).
func (p *progress) observe(id string, at time.Time, st engine.EngineStatus) ProgressItem {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.jobs[id]
	if s == nil {
		s = &jobSpeed{ema: float64(st.Down), at: at}
		p.jobs[id] = s
	} else if dt := at.Sub(s.at); dt > 0 {
		a := 1 - math.Exp2(-dt.Seconds()/speedHalfLife.Seconds())
		s.ema += a * (float64(st.Down) - s.ema)
		s.at = at
	}
	it := ProgressItem{ID: id, Done: st.Done, Total: st.Total, Down: int64(math.Round(s.ema)), Up: st.Up, Conns: st.Conns}
	if st.Total > 0 && s.ema >= 1 {
		eta := time.Duration(float64(st.Total-st.Done) / s.ema * float64(time.Second))
		it.ETA = &eta
	}
	s.item, s.st = it, st
	return it
}

func (p *progress) last(id string) (ProgressItem, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.jobs[id]; s != nil {
		return s.item, true
	}
	return ProgressItem{}, false
}

// lastStatus is the engine status of the job's latest sample.
func (p *progress) lastStatus(id string) (engine.EngineStatus, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.jobs[id]; s != nil {
		return s.st, true
	}
	return engine.EngineStatus{}, false
}

func (p *progress) forget(id string) {
	p.mu.Lock()
	delete(p.jobs, id)
	p.mu.Unlock()
}

// sample records the total speed and drops what is older than speedSpan.
func (p *progress) sample(at time.Time, down, up int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ring = append(p.ring, SpeedSample{At: at, Down: down, Up: up})
	cut := 0
	for cut < len(p.ring) && p.ring[cut].At.Before(at.Add(-speedSpan)) {
		cut++
	}
	p.ring = append(p.ring[:0], p.ring[cut:]...)
}

func (p *progress) samples() []SpeedSample {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]SpeedSample(nil), p.ring...)
}

// due reports whether a progress push fits within hz pushes per second,
// and if so counts one.
func (p *progress) due(now time.Time, hz int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if hz > 0 && now.Sub(p.sent) < time.Second/time.Duration(hz) {
		return false
	}
	p.sent = now
	return true
}

// overlay returns j with the latest polled byte counts, speeds and ETA,
// as a copy.
func (p *progress) overlay(j *store.Job) *store.Job {
	it, ok := p.last(j.ID)
	if !ok {
		return j
	}
	c := *j
	c.DoneBytes = &it.Done
	if it.Total > 0 {
		c.TotalBytes = &it.Total
	}
	c.SpeedDown, c.SpeedUp = it.Down, it.Up
	if it.ETA != nil {
		s := int64(it.ETA.Round(time.Second) / time.Second)
		c.ETASeconds = &s
	}
	return &c
}
