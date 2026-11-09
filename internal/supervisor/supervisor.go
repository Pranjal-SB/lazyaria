// Package supervisor runs aria2c as a child process: it writes the conf file,
// waits for the RPC endpoint, restarts aria2c with backoff and stops on a
// crash loop until told to retry.
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

// ErrEngineFailed means the supervisor gave up: aria2c is missing or too old,
// its config could not be written, or it kept dying right after start. The
// supervisor stays alive in the failed state; Restart tries again.
var ErrEngineFailed = errors.New("aria2 engine failed")

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
	s            *config.Settings
	p            config.Paths
	log          *slog.Logger
	policy       Policy
	pickPort     func() (int, error)
	readyTimeout time.Duration

	state   chan EngineState
	clients chan *aria2.Client
	restart chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	mu      sync.Mutex
	cur     *proc
	adopt   *Endpoint
	started bool
	failed  bool
}

// New prepares a supervisor. The runtime and state dirs must exist.
func New(s *config.Settings, p config.Paths, log *slog.Logger) *Supervisor {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	dur := func(key, v string, def time.Duration) time.Duration {
		d, err := config.ParseDuration(v)
		if err != nil || d <= 0 {
			log.Warn("invalid engine setting, using the default", "key", key, "value", v, "default", def)
			return def
		}
		return d
	}
	exits := s.Engine.CrashLoopExits
	if exits < 1 {
		exits = 5
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		s: s, p: p, log: log,
		policy: Policy{
			MaxBackoff: dur("engine.restart_max_backoff", s.Engine.RestartMaxBackoff, 30*time.Second),
			LoopExits:  exits,
			LoopWindow: dur("engine.crash_loop_window", s.Engine.CrashLoopWindow, 3*time.Second),
		},
		pickPort:     freePort,
		readyTimeout: 3 * time.Second,
		state:        make(chan EngineState, 16),
		clients:      make(chan *aria2.Client, 1),
		restart:      make(chan struct{}, 1),
		ctx:          ctx, cancel: cancel,
		done: make(chan struct{}),
	}
}

// State reports phase changes. If the reader falls behind, the oldest
// entries are dropped. It is closed by Shutdown.
func (sv *Supervisor) State() <-chan EngineState { return sv.state }

// Clients delivers the new client after each restart. Only the latest is
// kept. It is closed by Shutdown.
func (sv *Supervisor) Clients() <-chan *aria2.Client { return sv.clients }

// Start launches aria2c and returns a client once it answers. A failing
// aria2c is retried under the restart policy. On a crash loop Start returns
// ErrEngineFailed but the supervisor stays alive in the failed state: the
// caller must keep serving (State, Clients, Restart, Shutdown) rather than
// treat the error as fatal.
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

// Restart leaves the failed state and launches aria2c again with a fresh
// restart policy. The new client arrives on Clients.
func (sv *Supervisor) Restart() error {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.ctx.Err() != nil {
		return errors.New("supervisor is shut down; start a new one")
	}
	if !sv.failed {
		return errors.New("aria2c is not in the failed state; nothing to restart")
	}
	sv.failed = false
	sv.restart <- struct{}{} // buffered, and failed guards against a second send
	return nil
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
	push(sv.state, EngineState{Phase: PhaseStarting})
	for {
		tail, err := sv.supervise(&first)
		if sv.ctx.Err() != nil {
			if first != nil {
				first <- startResult{err: errShutdown}
			}
			return
		}
		// Every non-shutdown Start error must read as "alive and failed, call
		// Restart", including a wrong aria2c_path the user can fix.
		if !errors.Is(err, ErrEngineFailed) {
			err = fmt.Errorf("%w: %w", ErrEngineFailed, err)
		}
		sv.mu.Lock()
		sv.failed = true
		sv.mu.Unlock()
		push(sv.state, EngineState{Phase: PhaseFailed, LogTail: tail})
		sv.log.Error("aria2c failed; waiting for a manual restart", "err", err)
		if first != nil {
			first <- startResult{err: err}
			first = nil
		}
		select {
		case <-sv.restart:
			sv.policy.reset()
			push(sv.state, EngineState{Phase: PhaseStarting})
		case <-sv.ctx.Done():
			return
		}
	}
}

// supervise keeps aria2c running until it fails for good or ctx ends. The
// first ready client goes to *first; later ones go to Clients.
func (sv *Supervisor) supervise(first *chan<- startResult) ([]string, error) {
	if _, err := aria2.CheckVersion(sv.settings().Engine.Aria2cPath); err != nil {
		return []string{err.Error()}, err
	}
	for {
		var pr *proc
		var c *aria2.Client
		var err error
		sv.mu.Lock()
		ep := sv.adopt
		sv.adopt = nil
		sv.mu.Unlock()
		if ep != nil {
			if pr, c, err = sv.takeOver(*ep); err != nil {
				sv.log.Warn("could not take over the running aria2c; starting a new one", "err", err)
			}
		}
		if c == nil {
			pr, c, err = sv.launch()
		}
		ready := c != nil
		if ready {
			if *first != nil {
				*first <- startResult{c: c}
				*first = nil
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
			return nil, nil
		}
		if pr == nil { // nothing ran: a config or filesystem problem a retry won't fix
			return []string{err.Error()}, err
		}
		exitedAt := pr.exitedAt
		if !ready { // a launch that never answered counts as a fast exit however long it hung
			exitedAt = pr.started
		}
		delay, giveUp := sv.policy.OnExit(pr.started, exitedAt)
		if giveUp {
			tail := sv.logTail()
			last := "no output"
			if len(tail) > 0 {
				last = tail[len(tail)-1]
			}
			if err == nil {
				err = pr.err
			}
			return tail, fmt.Errorf("%w: aria2c failed %d times in a row right after start (last error: %v; last log line: %s); see %s and run 'lazyaria doctor'",
				ErrEngineFailed, sv.policy.LoopExits, err, last, sv.logPath())
		}
		sv.log.Warn("aria2c exited, restarting", "err", err, "exit", pr.err, "uptime", pr.exitedAt.Sub(pr.started), "delay", delay)
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
