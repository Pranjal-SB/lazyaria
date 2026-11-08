package daemon

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestReconcile_IntentRowWithoutGID(t *testing.T) {
	r := newRig(t)
	j := r.insert(t, store.Queued, "", nil)
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 1 {
		t.Fatalf("adds %d, want 1", adds)
	}
	if got := r.get(t, j.ID); got.EngineRef == nil || *got.EngineRef != "g1" {
		t.Fatalf("ref %v", got.EngineRef)
	}
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 1 {
		t.Fatalf("second reconcile re-added: %d adds", adds)
	}
}

func TestReconcile_ReaddUnknownGID(t *testing.T) {
	r := newRig(t)
	out := "f.bin"
	act := r.insert(t, store.Active, "old1", func(j *store.Job) { j.Out = &out })
	pau := r.insert(t, store.Paused, "old2", func(j *store.Job) {
		j.URIs = []string{"http://example.com/g.bin"}
	})
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if len(r.e.adds) != 2 {
		t.Fatalf("adds %d, want 2", len(r.e.adds))
	}
	byURI := map[string]int{}
	for i, a := range r.e.adds {
		byURI[a.URIs[0]] = i
		if a.Dir != r.dir {
			t.Fatalf("dir %q", a.Dir)
		}
	}
	a := r.e.adds[byURI["http://example.com/f.bin"]]
	if a.Out != "f.bin" || a.Paused {
		t.Fatalf("active re-add: out %q paused %v", a.Out, a.Paused)
	}
	if !r.e.adds[byURI["http://example.com/g.bin"]].Paused {
		t.Fatal("paused job re-added running")
	}
	if got := r.get(t, act.ID); *got.EngineRef == "old1" {
		t.Fatal("new ref not stored")
	}
	if got := r.get(t, pau.ID); got.State != store.Paused {
		t.Fatalf("paused job became %s", got.State)
	}
}

func TestReconcile_ConfirmsBeforeReadd(t *testing.T) {
	r := newRig(t)
	j := r.insert(t, store.Active, "", nil)
	// The engine has it, but List missed it while it moved between buckets.
	r.e.setState("moving", "active")
	ref := "moving"
	r.st.UpdateJob(bg, j.ID, func(x *store.Job) error { x.EngineRef = &ref; return nil })
	r.m.e = listMisses{r.e}
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 0 {
		t.Fatalf("re-added a download the engine still has: %d adds", adds)
	}
}

func TestReconcile_DirMissing(t *testing.T) {
	r := newRig(t)
	moved := r.insert(t, store.Active, "old1", func(j *store.Job) { d := uint64(99); j.DirDev = &d })
	gone := r.insert(t, store.Queued, "", func(j *store.Job) { j.Dir = "/nonexistent/x" })
	r.m.dev = func(dir string) (uint64, error) {
		if dir == "/nonexistent/x" {
			return 0, errors.New("no such dir")
		}
		return 7, nil
	}
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 0 {
		t.Fatalf("adds %d, want 0", adds)
	}
	for _, j := range []*store.Job{moved, gone} {
		got := r.get(t, j.ID)
		if got.State != store.Error || *got.ErrorKind != "dir-missing" {
			t.Fatalf("%s: %s %v", j.Dir, got.State, got.ErrorKind)
		}
	}
}

func TestReconcile_RearmsRetry(t *testing.T) {
	r := newRig(t)
	past := r.clk.now().Add(-time.Minute)
	j := r.insert(t, store.RetryWait, "old1", func(j *store.Job) { j.RetryAt = &past; j.RetryCount = 2 })
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for r.get(t, j.ID).State != store.Queued {
		if time.Now().After(deadline) {
			t.Fatal("re-armed retry did not fire")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.get(t, j.ID); got.RetryCount != 2 {
		t.Fatalf("retry count %d, want 2", got.RetryCount)
	}
}

// listMisses is an engine whose List never sees anything, like a page that
// raced a download moving between buckets.
type listMisses struct{ *fakeEngine }

func (listMisses) List(context.Context) ([]engine.EngineStatus, error) { return nil, nil }
