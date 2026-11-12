package daemon

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Event is a reliable lifecycle event pushed to subscribers.
type Event struct {
	Seq  uint64         `json:"seq"`
	Type string         `json:"type"` // job.added|job.updated|job.removed|engine.state|banner|resync_required
	Job  *store.Job     `json:"job,omitempty"`
	Data map[string]any `json:"data,omitempty"`
}

// ProgressItem is one job's lossy progress sample.
// On the wire ETA is whole seconds, see json.go.
type ProgressItem struct {
	ID                           string
	Done, Total, Down, Up, Conns int64
	ETA                          *time.Duration
}

// Bus fans events out to subscribers without ever blocking the publisher.
type Bus struct {
	seq   atomic.Uint64
	queue int
	mu    sync.Mutex
	subs  map[*Sub]struct{}
}

func NewBus(queue int) *Bus {
	if queue <= 0 {
		queue = 512
	}
	return &Bus{queue: queue, subs: map[*Sub]struct{}{}}
}

// Sub is one subscriber's view of the bus.
type Sub struct {
	bus      *Bus
	want     bool
	events   chan Event
	progress chan []ProgressItem
	closed   bool // guarded by bus.mu
	resync   bool // resync_required is queued and unread; guarded by bus.mu
}

func (b *Bus) Subscribe(wantProgress bool) *Sub {
	s := &Sub{
		bus:      b,
		want:     wantProgress,
		events:   make(chan Event, b.queue),
		progress: make(chan []ProgressItem, 1),
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

func (s *Sub) Events() <-chan Event            { return s.events }
func (s *Sub) Progress() <-chan []ProgressItem { return s.progress }

// Close unsubscribes and closes both channels; safe to call repeatedly.
func (s *Sub) Close() {
	b := s.bus
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	delete(b.subs, s)
	close(s.events)
	close(s.progress)
}

// Publish stamps ev with the next Seq and queues it for every subscriber.
// A subscriber whose queue is full loses its backlog and gets a single
// resync_required instead, so it knows to refetch state.
func (b *Bus) Publish(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ev.Seq = b.seq.Add(1)
	for s := range b.subs {
		if s.resync {
			if len(s.events) > 0 {
				continue // the pending resync already covers this event
			}
			s.resync = false
		}
		select {
		case s.events <- ev:
			continue
		default:
		}
		for drained := false; !drained; {
			select {
			case <-s.events:
			default:
				drained = true
			}
		}
		s.events <- Event{Seq: b.seq.Add(1), Type: "resync_required"} // queue is empty, cannot block
		s.resync = true
	}
}

// PublishProgress replaces any pending progress value for subscribers that want it.
func (b *Bus) PublishProgress(items []ProgressItem) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		if !s.want {
			continue
		}
		select {
		case <-s.progress:
		default:
		}
		s.progress <- items // only writers hold b.mu, so the slot is free
	}
}
