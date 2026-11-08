package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
)

// callTimeout bounds a call whose caller gave no deadline: a lost reply
// must not hang a job forever.
const callTimeout = 10 * time.Second

type aria2Engine struct {
	sv          *supervisor.Supervisor // nil in attach mode
	url, secret string

	events chan EngineEvent
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once

	// pendingResync is only touched by the follow goroutine.
	pendingResync bool

	mu      sync.Mutex
	c       *aria2.Client
	started bool
	closed  bool
}

// NewAria2 drives an aria2c owned by sv, following it across restarts.
func NewAria2(sv *supervisor.Supervisor) Engine { return newAria2(sv, "", "") }

// NewAria2Attach talks to an aria2 someone else runs. It never shuts it down.
func NewAria2Attach(url, secret string) Engine { return newAria2(nil, url, secret) }

func newAria2(sv *supervisor.Supervisor, url, secret string) *aria2Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &aria2Engine{
		sv: sv, url: url, secret: secret,
		events: make(chan EngineEvent, 256),
		ctx:    ctx, cancel: cancel,
		done: make(chan struct{}),
	}
}

// Start returns once aria2 answers. In supervised mode a failed start
// still follows the supervisor, so a later Restart brings the engine back.
func (e *aria2Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return errors.New("engine: already shut down; create a new one")
	}
	if e.started {
		e.mu.Unlock()
		return errors.New("engine: Start called twice")
	}
	e.started = true
	e.mu.Unlock()
	var c *aria2.Client
	var err error
	if e.sv != nil {
		c, err = e.sv.Start(ctx)
	} else {
		c, err = aria2.Dial(ctx, e.url, e.secret, aria2.Options{})
		if err != nil {
			close(e.done)
			return err
		}
	}
	e.mu.Lock()
	e.c = c
	e.mu.Unlock()
	go e.follow(c)
	return err
}

func (e *aria2Engine) Events() <-chan EngineEvent { return e.events }

// Shutdown stops a supervised aria2c; an attached one is only disconnected.
func (e *aria2Engine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	var err error
	if e.sv != nil {
		err = e.sv.Shutdown(ctx)
	} else if c, cerr := e.client(); cerr == nil {
		err = c.Close()
	}
	e.cancel()
	e.mu.Lock()
	started := e.started
	e.mu.Unlock()
	if started {
		<-e.done
	}
	e.once.Do(func() { close(e.events) })
	return err
}

var kinds = map[string]string{
	"aria2.onDownloadStart":      KindStart,
	"aria2.onDownloadPause":      KindPause,
	"aria2.onDownloadStop":       KindStop,
	"aria2.onDownloadComplete":   KindComplete,
	"aria2.onDownloadError":      KindError,
	"aria2.onBtDownloadComplete": KindBTComplete,
}

// follow turns notifications into events. In supervised mode it swaps to
// each new client from the supervisor until its Clients channel closes.
func (e *aria2Engine) follow(c *aria2.Client) {
	defer close(e.done)
	var swaps <-chan *aria2.Client
	if e.sv != nil {
		swaps = e.sv.Clients()
	}
	var notes <-chan aria2.Notification
	var resync <-chan struct{}
	if c != nil {
		notes, resync = c.Notifications(), c.Resync()
	}
	for {
		if notes == nil && swaps == nil {
			return
		}
		var flush chan<- EngineEvent
		if e.pendingResync {
			flush = e.events
		}
		select {
		case flush <- EngineEvent{Kind: KindResync}:
			e.pendingResync = false
		case n, ok := <-notes:
			if !ok {
				notes, resync = nil, nil
				continue
			}
			if k, known := kinds[n.Method]; known {
				e.emit(EngineEvent{Ref: EngineRef(n.GID), Kind: k})
			}
		case _, ok := <-resync:
			if !ok {
				resync = nil
				continue
			}
			e.emit(EngineEvent{Kind: KindResync})
		case nc, ok := <-swaps:
			if !ok {
				return
			}
			e.mu.Lock()
			e.c = nc
			e.mu.Unlock()
			notes, resync = nc.Notifications(), nc.Resync()
			e.emit(EngineEvent{Kind: KindResync})
		case <-e.ctx.Done():
			return
		}
	}
}

// emit never blocks, so a slow reader cannot hold up a client swap. When
// the channel is full the event is dropped and one resync goes out as soon
// as there is room, telling the reader to refetch.
func (e *aria2Engine) emit(ev EngineEvent) {
	if e.pendingResync {
		select {
		case e.events <- EngineEvent{Kind: KindResync}:
			e.pendingResync = false
		default:
			return
		}
		if ev.Kind == KindResync {
			return
		}
	}
	select {
	case e.events <- ev:
	default:
		e.pendingResync = true
	}
}

func (e *aria2Engine) client() (*aria2.Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.c == nil {
		return nil, ErrNotRunning
	}
	return e.c, nil
}

func (e *aria2Engine) call(ctx context.Context, method string, out any, params ...any) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	ctx, cancel := withDefault(ctx)
	defer cancel()
	if err := c.Call(ctx, method, params, out); err != nil {
		if errors.Is(err, aria2.ErrDisconnected) {
			return ErrNotRunning
		}
		return err
	}
	return nil
}

func isNotFound(err error) bool {
	var re *aria2.RPCError
	return errors.As(err, &re) && strings.Contains(re.Message, "is not found")
}
