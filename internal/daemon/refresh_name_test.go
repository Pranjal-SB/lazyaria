package daemon

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// A job keeps the name it shows: refreshing one that never started with a
// link to a differently named file only swaps the link, both on a re-add
// and when aria2 picks the name from the new link itself.
func TestRefreshKeepsTheJobsName(t *testing.T) {
	for _, lost := range []bool{false, true} {
		r := newRig(t)
		wfile(t, filepath.Join(r.dir, "ubuntu.iso"), "mine")
		wfile(t, filepath.Join(r.dir, "ubuntu.1.iso"), "mine too")
		j := r.add(t, AddParams{URIs: []string{"http://example.com/missing.zip"}})
		ref := r.ref(t, j.ID)
		r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.ErrorCode, s.HTTPStatus = "error", 3, 404 })
		r.event(ref, engine.KindError)
		if lost {
			// The engine forgot it: refresh re-adds from the row.
			r.e.Remove(bg, ref)
			r.e.changeErr = errors.New("no such download")
		}
		got, err := r.m.RefreshURI(bg, j.ID, "http://example.com/ubuntu.iso")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "missing.zip" {
			t.Fatalf("lost %v: refresh renamed the job to %q", lost, got.Name)
		}
		if lost {
			if add := r.e.adds[len(r.e.adds)-1]; add.Out != "missing.zip" {
				t.Fatalf("re-add out %q", add.Out)
			}
			continue
		}
		// aria2 names the file from the new link.
		wfile(t, filepath.Join(stagingDir(j), "ubuntu.iso"), "data")
		r.e.set(r.ref(t, j.ID), func(s *engine.EngineStatus) {
			s.State, s.ErrorCode, s.HTTPStatus = "active", 0, 0
			s.Path, s.Done, s.Total = filepath.Join(stagingDir(j), "ubuntu.iso"), 2, 4
		})
		r.event(r.ref(t, j.ID), engine.KindStart)
		done := r.complete(t, r.get(t, j.ID), "ubuntu.iso")
		if done.State != store.Done || done.Name != "missing.zip" || read(t, filepath.Join(r.dir, "missing.zip")) != "data" {
			t.Fatalf("finished %s as %q", done.State, done.Name)
		}
	}
}
