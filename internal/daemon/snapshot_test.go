package daemon

import (
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestSnapshotOnlyAskedIDs(t *testing.T) {
	r := newRig(t)
	a := r.add(t, AddParams{URIs: []string{"http://example.com/a"}})
	r.add(t, AddParams{URIs: []string{"http://example.com/b"}})
	js := r.m.Snapshot(bg, store.Filter{IncludeHistory: true, IDs: []string{a.ID}}).Jobs
	if len(js) != 1 || js[0].ID != a.ID {
		t.Fatalf("got %d jobs, want only %s", len(js), a.ID)
	}
	if all := r.m.Snapshot(bg, store.Filter{IncludeHistory: true}).Jobs; len(all) != 2 {
		t.Fatalf("no ids: got %d jobs, want 2", len(all))
	}
}
