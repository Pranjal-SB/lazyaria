package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

type recordFiles struct{ got []string }

func (f *recordFiles) RemoveFiles(_ context.Context, j *store.Job) error {
	f.got = append(f.got, j.ID)
	return nil
}

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
