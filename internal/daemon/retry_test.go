package daemon

import (
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestBackoffDelay(t *testing.T) {
	b := Backoff{Base: 30 * time.Second, Factor: 2, Max: 5, Jitter: 0.2}
	if d := b.Delay(1, func() float64 { return 0 }); d != 24*time.Second {
		t.Fatalf("low jitter %v", d)
	}
	if d := b.Delay(1, func() float64 { return 1 }); d != 36*time.Second {
		t.Fatalf("high jitter %v", d)
	}
}

// fail429 puts the job's current download into a 429 error.
func (r *rig) fail429(t *testing.T, id string) {
	t.Helper()
	ref := r.ref(t, id)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.ErrorCode, s.HTTPStatus = "error", 22, 429 })
	r.event(ref, engine.KindError)
}

func TestBackoffSchedule(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	for i, want := range []time.Duration{30, 60, 120, 240, 480} {
		r.fail429(t, j.ID)
		got := r.get(t, j.ID)
		if got.State != store.RetryWait || got.RetryCount != i+1 {
			t.Fatalf("attempt %d: %s count %d", i+1, got.State, got.RetryCount)
		}
		if d := got.RetryAt.Sub(r.clk.now()); d != want*time.Second {
			t.Fatalf("attempt %d: delay %v, want %vs", i+1, d, want)
		}
		r.m.fireRetry(j.ID) // what the timer does when it is due
		if got := r.get(t, j.ID); got.State != store.Queued || got.RetryAt != nil {
			t.Fatalf("after retry %d: %s", i+1, got.State)
		}
	}
	if adds, _ := r.e.count(); adds != 6 {
		t.Fatalf("engine adds %d, want 6", adds)
	}
	r.fail429(t, j.ID)
	got := r.get(t, j.ID)
	if got.State != store.Error || *got.ErrorKind != "rate-limited" {
		t.Fatalf("after 6th failure: %s %v", got.State, got.ErrorKind)
	}
}

func TestRetryRespectsUserPause(t *testing.T) {
	r := newRig(t)
	r.m.s.Retry.BackoffBase = "50ms"
	r.m.now = time.Now
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.fail429(t, j.ID)
	if err := r.m.Pause(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if adds, _ := r.e.count(); adds != 1 {
		t.Fatalf("re-added while paused: %d adds", adds)
	}
	got := r.get(t, j.ID)
	if got.State != store.Paused || got.RetryAt != nil {
		t.Fatalf("state %s retry_at %v", got.State, got.RetryAt)
	}
	if err := r.m.Resume(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 2 {
		t.Fatalf("resume did not re-add: %d adds", adds)
	}
}

func TestRetryTimerFires(t *testing.T) {
	r := newRig(t)
	r.m.s.Retry.BackoffBase = "20ms"
	r.m.now = time.Now
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.fail429(t, j.ID)
	deadline := time.Now().Add(2 * time.Second)
	for r.get(t, j.ID).State != store.Queued {
		if time.Now().After(deadline) {
			t.Fatal("retry timer never fired")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
