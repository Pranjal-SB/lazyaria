package daemon

import (
	"path/filepath"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
)

// aria2 renamed the download because the user already has a.iso, and no
// poll has caught up yet. Removing it with its files must take a.1.iso and
// leave the user's a.iso alone.
func TestRemoveDuringAutoRename(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso", Force: true})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	mine, ours := filepath.Join(r.dir, "a.iso"), filepath.Join(r.dir, "a.1.iso")
	wfile(t, mine, "the user's")
	wfile(t, ours, "data")
	wfile(t, ours+".aria2", "ctl")
	r.e.set(ref, func(s *engine.EngineStatus) { s.Path, s.Done, s.Total = ours, 4, 100 })

	if _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil {
		t.Fatal(err)
	}
	if !exists1(mine) {
		t.Fatal("the user's a.iso was trashed")
	}
	if exists1(ours) || exists1(ours+".aria2") {
		t.Fatalf("left behind: a.1.iso %v, control file %v", exists1(ours), exists1(ours+".aria2"))
	}
}

// The live byte count must reach the removed row, or the row Undo brings
// back no longer owns its file and a second remove leaves the partial.
func TestRemoveUndoRemoveTrashesPartial(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	payload := filepath.Join(r.dir, "a.iso")
	wfile(t, payload, "data")
	wfile(t, payload+".aria2", "ctl")
	r.e.set(ref, func(s *engine.EngineStatus) { s.Done, s.Total = 4, 100 })

	tok, err := r.m.Remove(bg, []string{j.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if !exists1(payload) {
		t.Fatal("undo did not bring the payload back")
	}
	if _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil {
		t.Fatal(err)
	}
	if exists1(payload) || exists1(payload+".aria2") {
		t.Fatalf("left behind: payload %v, control file %v", exists1(payload), exists1(payload+".aria2"))
	}
}
