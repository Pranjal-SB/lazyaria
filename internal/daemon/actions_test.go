package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

type recordFiles struct{ got []string }

func (f *recordFiles) RemoveFiles(_ context.Context, j *store.Job, _ []string) ([]trash.Entry, error) {
	f.got = append(f.got, j.ID)
	return nil, nil
}

func (f *recordFiles) Check(context.Context, *store.Job, []string) error { return nil }

func (f *recordFiles) RestoreFiles(context.Context, []trash.Entry) error { return nil }

func TestRemoveAndUndo(t *testing.T) {
	r := newRig(t)
	files := &recordFiles{}
	r.m.Files = files
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	tok, err := r.m.Remove(bg, []string{j.ID}, true)
	if err != nil || tok == "" {
		t.Fatalf("remove: %q %v", tok, err)
	}
	if got := r.get(t, j.ID); got.State != store.Removed || got.RemovedAt == nil {
		t.Fatalf("state %s", got.State)
	}
	if len(r.e.removed) != 1 || len(files.got) != 1 {
		t.Fatalf("engine removes %v, file removes %v", r.e.removed, files.got)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	got := r.get(t, j.ID)
	if got.State != store.Paused || got.RemovedAt != nil || got.EngineRef != nil {
		t.Fatalf("after undo: %s ref %v", got.State, got.EngineRef)
	}
	if err := r.m.Undo(bg, tok); err == nil {
		t.Fatal("undo twice")
	}
	tok, _ = r.m.Remove(bg, []string{j.ID}, false)
	r.clk.add(11 * time.Second)
	if err := r.m.Undo(bg, tok); err == nil {
		t.Fatal("undo after the window")
	}
}

func TestStopSeeding(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{Torrent: []byte("d4:infod4:name1:xee")})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.Seeder = "active", true })
	r.event(ref, engine.KindBTComplete)
	if err := r.m.StopSeeding(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	if got := r.get(t, j.ID); got.State != store.Done {
		t.Fatalf("state %s", got.State)
	}
}

func TestRefreshErroredJob(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.ErrorCode, s.HTTPStatus = "error", 22, 403 })
	r.event(ref, engine.KindError)
	got, err := r.m.RefreshURI(bg, j.ID, "https://u:p@mirror.example.com/a.iso?t=2")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.Queued || got.ErrorKind != nil || got.URIs[0] != "https://mirror.example.com/a.iso?t=2" {
		t.Fatalf("after refresh: %s %v %v", got.State, got.ErrorKind, got.URIs)
	}
	if got.Options["http-user"] != "u" {
		t.Fatal("refresh credentials not kept for re-adds")
	}
	if _, err := r.m.RefreshURI(bg, j.ID, "file:///etc/passwd"); err == nil {
		t.Fatal("accepted a file: link")
	}
}

func TestPauseHoldsWhileEngineSettles(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	if err := r.m.Pause(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	r.e.setState(ref, "active") // aria2 still finishing the pause
	r.event(ref, engine.KindStart)
	if got := r.get(t, j.ID); got.State != store.Paused {
		t.Fatalf("state %s, want paused", got.State)
	}
}

func TestRefreshNewRef(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.e.changeRef = "00000000000000cc" // aria2 refused the old gid on re-add
	got, err := r.m.RefreshURI(bg, j.ID, "http://mirror.example.com/a.iso")
	if err != nil || *got.EngineRef != "00000000000000cc" {
		t.Fatalf("ref %v err %v", got.EngineRef, err)
	}
	if adds, _ := r.e.count(); adds != 1 {
		t.Fatalf("adds %d, want 1", adds)
	}
}

func TestRefreshLostDownloadReadds(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.Remove(bg, ref) // the engine lost it
	r.e.changeErr = errors.New("is not found")
	got, err := r.m.RefreshURI(bg, j.ID, "http://mirror.example.com/a.iso")
	if err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 2 || r.e.adds[1].URIs[0] != "http://mirror.example.com/a.iso" {
		t.Fatalf("adds %d %+v", adds, r.e.adds)
	}
	if *got.EngineRef != string(ref) || got.State != store.Queued {
		t.Fatalf("ref %s state %s", *got.EngineRef, got.State)
	}
}

func TestRemoveCancelsRetry(t *testing.T) {
	r := newRig(t)
	r.m.s.Load().Retry.BackoffBase = "50ms"
	r.m.now = time.Now
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.fail429(t, j.ID)
	if _, err := r.m.Remove(bg, []string{j.ID}, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if adds, _ := r.e.count(); adds != 1 {
		t.Fatalf("re-added after remove: %d adds", adds)
	}
	r.m.mu.Lock()
	n := len(r.m.retries)
	r.m.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d retry timers left", n)
	}
}

func TestPausedRowNeverAutoRetries(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, StartPaused: true})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.ErrorCode, s.HTTPStatus = "error", 22, 429 })
	r.event(ref, engine.KindError)
	if got := r.get(t, j.ID); got.State != store.Error {
		t.Fatalf("state %s, want error", got.State)
	}
}

func TestBatchSkipsAfterEngineDown(t *testing.T) {
	r := newRig(t)
	var ids []string
	for _, u := range []string{"a", "b", "c"} {
		j := r.add(t, AddParams{URIs: []string{"http://example.com/" + u}})
		ids = append(ids, j.ID)
	}
	down := &pauseDown{fakeEngine: r.e}
	r.m.e = down
	err := r.m.Pause(bg, ids)
	if !errors.Is(err, engine.ErrNotRunning) || down.pauses != 1 || !strings.Contains(err.Error(), "skipped") {
		t.Fatalf("err %v, pauses tried %d", err, down.pauses)
	}
}

// pauseDown fails pauses as if aria2 went away; status reads still work.
type pauseDown struct {
	*fakeEngine
	pauses int
}

func (p *pauseDown) Pause(context.Context, engine.EngineRef) error {
	p.pauses++
	return engine.ErrNotRunning
}
