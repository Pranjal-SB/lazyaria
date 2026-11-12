package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if got := r.get(t, j.ID); got.EngineRef == nil || *got.EngineRef != r.e.adds[0].GID || !gidRe.MatchString(*got.EngineRef) {
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
	if got := r.get(t, act.ID); *got.EngineRef != "old1" || a.GID != "old1" {
		t.Fatalf("ref %s, gid asked %s: want the stable old1", *got.EngineRef, a.GID)
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
	started := r.clk.now()
	out := "f.bin"
	// Started, device changed and its files are gone: the drive is not mounted.
	moved := r.insert(t, store.Active, "old1", func(j *store.Job) {
		d := uint64(99)
		j.DirDev, j.StartedAt, j.Out = &d, &started, &out
	})
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

func TestReconcile_DevChangeAloneIsNotMissing(t *testing.T) {
	r := newRig(t)
	started := r.clk.now()
	out := "f.bin"
	os.WriteFile(filepath.Join(r.dir, "f.bin.aria2"), []byte("x"), 0o600)
	withFile := r.insert(t, store.Active, "old1", func(j *store.Job) {
		d := uint64(99)
		j.DirDev, j.StartedAt, j.Out = &d, &started, &out
	})
	notStarted := r.insert(t, store.Queued, "old2", func(j *store.Job) {
		d := uint64(99)
		j.DirDev = &d
		j.URIs = []string{"http://example.com/g.bin"}
	})
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 2 {
		t.Fatalf("adds %d, want 2", adds)
	}
	for _, j := range []*store.Job{withFile, notStarted} {
		got := r.get(t, j.ID)
		if got.State == store.Error || got.DirDev == nil || *got.DirDev != 7 {
			t.Fatalf("%s: state %s dev %v", j.ID, got.State, got.DirDev)
		}
	}
}

func TestReconcile_AdoptsRunningGID(t *testing.T) {
	r := newRig(t)
	// A crash or timeout right after the engine accepted the add.
	j := r.insert(t, store.Queued, "00000000000000aa", nil)
	r.e.setState("00000000000000aa", "active")
	r.m.e = listMisses{r.e}
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 0 {
		t.Fatalf("adds %d, want 0", adds)
	}
	if got := r.get(t, j.ID); got.State != store.Active || *got.EngineRef != "00000000000000aa" {
		t.Fatalf("state %s ref %s", got.State, *got.EngineRef)
	}
}

func TestReconcile_RemovedResultIsLost(t *testing.T) {
	r := newRig(t)
	j := r.insert(t, store.Active, "old1", nil)
	r.e.setState("old1", "removed")
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 1 || r.get(t, j.ID).State != store.Queued {
		t.Fatalf("adds %d, state %s", adds, r.get(t, j.ID).State)
	}
}

func TestReconcile_StopsWhenEngineDown(t *testing.T) {
	r := newRig(t)
	for range 3 {
		r.insert(t, store.Active, "", nil)
	}
	down := &downEngine{fakeEngine: r.e}
	r.m.e = down
	err := r.m.Reconcile(bg)
	if !errors.Is(err, engine.ErrNotRunning) || down.adds != 1 {
		t.Fatalf("err %v, adds tried %d", err, down.adds)
	}
}

// downEngine lists fine but fails every add as if aria2 just went away.
type downEngine struct {
	*fakeEngine
	adds int
}

func (d *downEngine) Add(context.Context, engine.AddRequest) (engine.EngineRef, error) {
	d.adds++
	return "", engine.ErrNotRunning
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

func TestAttachRespectsExternalRemove(t *testing.T) {
	r := newRig(t)
	r.m.Attach = true
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "removed") // someone removed it through aria2 directly
	r.m.poll(bg)
	got := r.get(t, j.ID)
	if got.State != store.Removed || got.RemovedAt == nil || !r.hasEvent(t, j.ID, "removed-externally") {
		t.Fatalf("state %s", got.State)
	}
	if adds, _ := r.e.count(); adds != 1 {
		t.Fatalf("adds %d, want only the first", adds)
	}
}

func TestSupervisedRemovedRowNotReadded(t *testing.T) {
	r := newRig(t)
	j := r.insert(t, store.Removed, "old1", nil)
	r.e.setState("old1", "removed")
	if err := r.m.gone(bg, j, engine.EngineStatus{Ref: "old1", Known: true, State: "removed"}); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 0 {
		t.Fatalf("adds %d, want 0", adds)
	}
	// A live row with a removed result is still ours to re-add.
	live := r.insert(t, store.Active, "old2", nil)
	r.e.setState("old2", "removed")
	r.m.poll(bg)
	if adds, _ := r.e.count(); adds != 1 || r.get(t, live.ID).State != store.Queued {
		t.Fatalf("adds %d", adds)
	}
}

func TestRetryIntoMissingDir(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.fail429(t, j.ID)
	r.m.dev = func(string) (uint64, error) { return 0, errors.New("unmounted") }
	r.m.fireRetry(j.ID)
	got := r.get(t, j.ID)
	if got.State != store.Error || *got.ErrorKind != "dir-missing" || *got.DirDev != 7 {
		t.Fatalf("state %s kind %v dev %v", got.State, got.ErrorKind, got.DirDev)
	}
	if adds, _ := r.e.count(); adds != 1 {
		t.Fatalf("adds %d, want only the first", adds)
	}
}

func TestCompletedOfflineNotRedownloaded(t *testing.T) {
	r := newRig(t)
	started := r.clk.now()
	out := "full.bin"
	total := int64(5)
	os.WriteFile(filepath.Join(r.dir, out), []byte("12345"), 0o600)
	wrote := int64(3)
	j := r.insert(t, store.Active, "old1", func(j *store.Job) {
		j.Out, j.StartedAt, j.TotalBytes, j.DoneBytes = &out, &started, &total, &wrote
	})
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 0 {
		t.Fatalf("adds %d, want 0", adds)
	}
	if got := r.get(t, j.ID); got.State != store.Done || !r.hasEvent(t, j.ID, "completed-offline") {
		t.Fatalf("state %s", got.State)
	}
	// A short file is a partial download and is re-added.
	short := "short.bin"
	os.WriteFile(filepath.Join(r.dir, short), []byte("12"), 0o600)
	k := r.insert(t, store.Active, "old2", func(j *store.Job) {
		j.Out, j.StartedAt, j.TotalBytes, j.DoneBytes = &short, &started, &total, &wrote
	})
	r.m.Reconcile(bg)
	if adds, _ := r.e.count(); adds != 1 || r.get(t, k.ID).State != store.Queued {
		t.Fatalf("short file: adds %d", adds)
	}
	// A same-size file at Out that the job never wrote to is not adopted;
	// the re-add renames beside it.
	foreign := "foreign.bin"
	os.WriteFile(filepath.Join(r.dir, foreign), []byte("abcde"), 0o600)
	f := r.insert(t, store.Active, "old3", func(j *store.Job) { j.Out, j.StartedAt, j.TotalBytes = &foreign, &started, &total })
	r.m.Reconcile(bg)
	if adds, _ := r.e.count(); adds != 2 || r.get(t, f.ID).State == store.Done || !r.e.adds[1].NoContinue || r.e.adds[1].AllowOverwrite {
		t.Fatalf("foreign file: adds %d state %s", adds, r.get(t, f.ID).State)
	}
}
