package daemon

import "testing"

func TestSnapshotOnlyAskedIDs(t *testing.T) {
	r := newRig(t)
	a := r.add(t, AddParams{URIs: []string{"http://example.com/a"}})
	r.add(t, AddParams{URIs: []string{"http://example.com/b"}})
	_, js, _ := r.m.Snapshot(bg, true, 0, []string{a.ID}, nil)
	if len(js) != 1 || js[0].ID != a.ID {
		t.Fatalf("got %d jobs, want only %s", len(js), a.ID)
	}
	if _, all, _ := r.m.Snapshot(bg, true, 0, nil, nil); len(all) != 2 {
		t.Fatalf("no ids: got %d jobs, want 2", len(all))
	}
}
