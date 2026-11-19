package daemon

import (
	"path/filepath"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

func TestUndoRetrySkipsEntriesAlreadyRestored(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	payload := filepath.Join(stagingDir(j), "a.iso")
	wfile(t, payload, "data")
	r.wrote(t, j.ID)
	wfile(t, payload+".aria2", "ctl")
	tok, _, err := r.m.Remove(bg, []string{j.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	es := r.m.undo[tok].files[j.ID]
	if len(es) != 2 {
		t.Fatalf("want 2 trashed entries, got %d", len(es))
	}
	// the first entry came back on an earlier attempt, the second did not
	if err := trash.Restore(es[0]); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !exists1(payload) || !exists1(payload+".aria2") {
		t.Fatal("files not back")
	}
}
