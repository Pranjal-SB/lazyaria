package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/fsinfo"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Manager owns every job's lifecycle. Each state change is written to the
// store before anything else relies on it, so after a crash at any point
// Reconcile can bring the engine and the database back in line.
type Manager struct {
	e   engine.Engine
	st  *store.Store
	s   *config.Settings
	bus *Bus
	log *slog.Logger

	// Files trashes a job's payload for Remove with deleteFiles set.
	Files FileRemover
	// Attach is set when the daemon uses an aria2 it did not start. Someone
	// else may then remove downloads, and such a removal is respected
	// instead of re-added. Set it before Run.
	Attach bool

	now   func() time.Time
	free  func(dir string) (uint64, error)
	dev   func(dir string) (uint64, error)
	alloc func(dir string) string
	rnd   func() float64

	// mu serializes every state change, engine calls included, so a poll,
	// an event, a retry timer and a user action never interleave on a job.
	mu      sync.Mutex
	ctx     context.Context // for retry timers; set by Run
	retries map[string]*time.Timer
	undo    map[string]undoRecord

	observers atomic.Int64
	kick      chan struct{}
	prog      *progress
}

func NewManager(e engine.Engine, st *store.Store, s *config.Settings, bus *Bus, log *slog.Logger) *Manager {
	return &Manager{
		e: e, st: st, s: s, bus: bus, log: log,
		Files: &TrashFiles{},
		now:   time.Now, free: fsinfo.FreeBytes, dev: fsinfo.Dev, alloc: fsinfo.Allocation, rnd: mrand.Float64,
		ctx:     context.Background(),
		retries: map[string]*time.Timer{},
		undo:    map[string]undoRecord{},
		kick:    make(chan struct{}, 1),
		prog:    newProgress(),
	}
}

// DuplicateError means the link is already in the list or in history
// (Existing), or a finished file already sits at the target path (Path).
type DuplicateError struct {
	Existing *store.Job
	Path     string
}

func (e *DuplicateError) Error() string {
	if e.Existing == nil {
		return fmt.Sprintf("%s already exists; add it with --force to download it again, or choose another name", e.Path)
	}
	return fmt.Sprintf("%q is already in the list (%s); add it with --force to download it again", e.Existing.Name, e.Existing.State)
}

// SkippedError means reliability.duplicates is "skip" and nothing was
// added: the link is already known (Existing), or a finished file already
// sits at the target path (Path). Callers treat it as success.
type SkippedError struct {
	Existing *store.Job
	Path     string
}

func (e *SkippedError) Error() string {
	if e.Existing != nil {
		return fmt.Sprintf("%q is already in the list (%s); skipped (reliability.duplicates = skip)", e.Existing.Name, e.Existing.State)
	}
	return fmt.Sprintf("%s already exists; skipped (reliability.duplicates = skip)", e.Path)
}

// NoSpaceError means the target filesystem cannot hold the download.
type NoSpaceError struct {
	Dir        string
	Need, Free uint64
}

func (e *NoSpaceError) Error() string {
	return fmt.Sprintf("not enough free space in %s: need %d bytes, %d free; free some space or pick another directory", e.Dir, e.Need, e.Free)
}

var (
	// runningStates have a live download in the engine.
	runningStates = []store.State{store.Queued, store.Active, store.Seeding}
	// workStates keep the daemon busy.
	workStates = []store.State{store.Queued, store.Active, store.RetryWait, store.Seeding}
)

func running(s store.State) bool {
	return s == store.Queued || s == store.Active || s == store.Paused || s == store.Seeding
}

func refOf(j *store.Job) engine.EngineRef { return engine.EngineRef(*j.EngineRef) }

// newGID makes an aria2 gid: 16 hex digits. Jobs get theirs before the
// engine sees them, so a download whose add was never confirmed can still
// be found by ref.
func newGID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// engineDown reports errors after which further engine calls in the same
// batch would only wait out the same failure.
func engineDown(err error) bool {
	return errors.Is(err, engine.ErrNotRunning) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// change updates a job in one transaction, records kind in its audit trail
// when set, and publishes the new row. fn runs inside the transaction, so it
// must not call the store or the engine.
func (m *Manager) change(ctx context.Context, id, kind, detail string, fn func(*store.Job)) (*store.Job, error) {
	return m.changeAs(ctx, "job.updated", id, kind, detail, fn)
}

func (m *Manager) changeAs(ctx context.Context, typ, id, kind, detail string, fn func(*store.Job)) (*store.Job, error) {
	j, err := m.st.UpdateJob(ctx, id, func(j *store.Job) error { fn(j); return nil })
	if err != nil {
		return nil, fmt.Errorf("update job %s: %w", id, err)
	}
	if kind != "" {
		if err := m.st.AddEvent(ctx, id, kind, detail); err != nil {
			m.log.Warn("record job event", "job", id, "kind", kind, "err", err)
		}
	}
	m.bus.Publish(Event{Type: typ, Job: j})
	m.wake()
	return j, nil
}

// set is change for callers that carry on with the old row when the store
// fails; the failure is logged.
func (m *Manager) set(ctx context.Context, j *store.Job, kind, detail string, fn func(*store.Job)) *store.Job {
	j2, err := m.change(ctx, j.ID, kind, detail, fn)
	if err != nil {
		m.log.Error("job update lost", "job", j.ID, "kind", kind, "err", err)
		return j
	}
	return j2
}

// to moves j into state s, recording the state as the event kind.
func (m *Manager) to(ctx context.Context, j *store.Job, s store.State, detail string, fn func(*store.Job)) *store.Job {
	if s != store.Active && s != store.Queued && s != store.Seeding {
		m.prog.forget(j.ID)
	}
	j = m.set(ctx, j, string(s), detail, func(x *store.Job) {
		x.State = s
		if fn != nil {
			fn(x)
		}
	})
	if s == store.Done {
		m.stampOrigin(j)
	}
	return j
}

func (m *Manager) wake() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (m *Manager) banner(kind string, data map[string]any) {
	data["kind"] = kind
	m.bus.Publish(Event{Type: "banner", Data: data})
}

// ActiveCount is how many jobs still have work to do: queued, downloading,
// waiting to retry or seeding.
func (m *Manager) ActiveCount() int {
	js, err := m.st.ListJobs(context.Background(), store.Filter{States: workStates})
	if err != nil {
		m.log.Warn("count active jobs", "err", err)
	}
	return len(js)
}

// SetObservers tells the manager how many clients want progress pushes.
func (m *Manager) SetObservers(n int) {
	m.observers.Store(int64(n))
	m.wake()
}

// Snapshot returns the bus sequence, the job list with live progress and
// the recent speed history. The sequence is read first, so a client that
// applies events after it never misses a change.
func (m *Manager) Snapshot(ctx context.Context, includeHistory bool, limit int) (uint64, []*store.Job, []SpeedSample) {
	seq := m.bus.seq.Load()
	js, err := m.st.ListJobs(ctx, store.Filter{IncludeHistory: includeHistory, Limit: limit})
	if err != nil {
		m.log.Warn("snapshot", "err", err)
	}
	for i, j := range js {
		js[i] = m.prog.overlay(j)
	}
	return seq, js, m.prog.samples()
}
