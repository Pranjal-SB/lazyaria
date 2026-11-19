package daemon

import (
	"path/filepath"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

func TestUndoRetrySkipsEntriesAlreadyRestored(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	id, music := liveTorrent(t, r)
	a, b := filepath.Join(music, "a.flac"), filepath.Join(music, "sub", "b.flac")
	wfile(t, a, "a")
	wfile(t, b, "b")
	tok, _, err := r.m.Remove(bg, []string{id}, true)
	if err != nil {
		t.Fatal(err)
	}
	es := r.m.undo[tok].files[id]
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
	if !exists1(a) || !exists1(b) {
		t.Fatal("files not back")
	}
}
