// Package supervisor runs aria2c as a child process: it writes the conf file,
// waits for the RPC endpoint, restarts aria2c with backoff and gives up on a
// crash loop.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
	"github.com/Pranjal-SB/lazyaria/internal/config"
)

const (
	PhaseStarting   = "starting"
	PhaseReady      = "ready"
	PhaseRestarting = "restarting"
	PhaseFailed     = "failed"
)

// EngineState is published on every phase change. LogTail is set on failure.
type EngineState struct {
	Phase   string
	LogTail []string
}

var errShutdown = errors.New("aria2c start cancelled by shutdown")

type startResult struct {
	c   *aria2.Client
	err error
}

// Supervisor owns aria2c and every client it hands out: a client is closed
// when its aria2c exits, and all are closed by Shutdown.
type Supervisor struct {
	s        *config.Settings
	p        config.Paths
	log      *slog.Logger
	policy   Policy
	pickPort func() (int, error)

	state   chan EngineState
	clients chan *aria2.Client
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	mu      sync.Mutex
	cur     *proc
	started bool
}

// New prepares a supervisor. Settings must already be validated by config.Load.
// The runtime and state dirs must exist.
func New(s *config.Settings, p config.Paths, log *slog.Logger) *Supervisor {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	maxBackoff, _ := config.ParseDuration(s.Engine.RestartMaxBackoff)
	window, _ := config.ParseDuration(s.Engine.CrashLoopWindow)
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		s: s, p: p, log: log,
		policy:   Policy{MaxBackoff: maxBackoff, LoopExits: s.Engine.CrashLoopExits, LoopWindow: window},
		pickPort: freePort,
		state:    make(chan EngineState, 16),
		clients:  make(chan *aria2.Client, 1),
		ctx:      ctx, cancel: cancel,
		done: make(chan struct{}),
	}
}

// State reports phase changes. If the reader falls behind, the oldest
// entries are dropped. It is closed once the supervisor stops for good.
func (sv *Supervisor) State() <-chan EngineState { return sv.state }

// Clients delivers the new client after each restart. Only the latest is
// kept. It is closed once the supervisor stops for good.
func (sv *Supervisor) Clients() <-chan *aria2.Client { return sv.clients }

// Start launches aria2c and returns a client once it answers. A failing
// aria2c is retried under the restart policy, so this returns an error only
// on a crash loop, a setup problem, ctx expiry, or Shutdown.
func (sv *Supervisor) Start(ctx context.Context) (*aria2.Client, error) {
	sv.mu.Lock()
	if sv.started {
		sv.mu.Unlock()
		return nil, errors.New("supervisor: Start called twice")
	}
	sv.started = true
	sv.mu.Unlock()
	first := make(chan startResult, 1)
	go sv.run(first)
	select {
	case r := <-first:
		return r.c, r.err
	case <-ctx.Done():
		sv.Shutdown(context.Background())
		return nil, ctx.Err()
	}
}

// Shutdown stops aria2c: aria2.shutdown, then aria2.forceShutdown after 3 s,
// then SIGTERM after 5 s more. If ctx ends first aria2c is killed. The child
// is always reaped before this returns.
func (sv *Supervisor) Shutdown(ctx context.Context) error {
	sv.cancel()
	sv.mu.Lock()
	started := sv.started
	sv.mu.Unlock()
	if !started {
		return nil
	}
	select {
	case <-sv.done:
		return nil
	case <-ctx.Done():
		sv.mu.Lock()
		if sv.cur != nil {
			sv.cur.cmd.Process.Kill()
		}
		sv.mu.Unlock()
		<-sv.done
		return ctx.Err()
	}
}

func (sv *Supervisor) run(first chan<- startResult) {
	defer close(sv.done)
	defer close(sv.clients)
	defer close(sv.state)
	fail := func(err error, tail []string) {
		push(sv.state, EngineState{Phase: PhaseFailed, LogTail: tail})
		if first != nil {
			first <- startResult{err: err}
		}
	}

	push(sv.state, EngineState{Phase: PhaseStarting})
	if _, err := aria2.CheckVersion(sv.s.Engine.Aria2cPath); err != nil {
		fail(err, []string{err.Error()})
		return
	}
	for {
		pr, c, err := sv.launch()
		if c != nil {
			if first != nil {
				first <- startResult{c: c}
				first = nil
			} else {
				push(sv.clients, c)
			}
			push(sv.state, EngineState{Phase: PhaseReady})
			select {
			case <-pr.exited:
			case <-sv.ctx.Done():
				sv.stop(pr, c)
			}
			c.Close()
		}
		if sv.ctx.Err() != nil {
			if first != nil {
				first <- startResult{err: errShutdown}
			}
			return
		}
		if pr == nil { // nothing ran: a config or filesystem problem a retry won't fix
			fail(err, []string{err.Error()})
			return
		}
		delay, giveUp := sv.policy.OnExit(pr.started, pr.exitedAt)
		if giveUp {
			tail := sv.logTail()
			last := "no output"
			if len(tail) > 0 {
				last = tail[len(tail)-1]
			}
			fail(fmt.Errorf("aria2c keeps exiting right after start (%d times in a row; last log line: %s); see %s or run `lazyaria doctor`",
				sv.policy.LoopExits, last, sv.logPath()), tail)
			return
		}
		sv.log.Warn("aria2c exited, restarting", "exit", pr.err, "uptime", pr.exitedAt.Sub(pr.started), "delay", delay)
		push(sv.state, EngineState{Phase: PhaseRestarting})
		t := time.NewTimer(delay)
		select {
		case <-t.C:
		case <-sv.ctx.Done():
			t.Stop()
		}
	}
}

// push never blocks: when the buffer is full the oldest value is dropped.
// Only the run goroutine sends, so the retry loop terminates.
func push[T any](ch chan T, v T) {
	for {
		select {
		case ch <- v:
			return
		default:
			select {
			case <-ch:
			default:
			}
		}
	}
}
