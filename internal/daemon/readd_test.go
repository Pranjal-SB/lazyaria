package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// on_exists = overwrite trashes only a regular file; a directory there is
// left for publish to name. A trash failure leaves no job row behind.
func TestOverwriteOnlyRegularAndUndoneOnTrashFailure(t *testing.T) {
	r := newRig(t)
	r.m.s.Load().Reliability.Duplicates = "allow"
	r.m.s.Load().Downloads.OnExists = "overwrite"
	os.Mkdir(filepath.Join(r.dir, "d.bin"), 0o700)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/d.bin"}})
	if j.Name != "d.bin" || j.Out != nil {
		t.Fatalf("directory target: name %q out %v, want d.bin, no out (publish picks the free name)", j.Name, j.Out)
	}
	os.WriteFile(filepath.Join(r.dir, "f.bin"), []byte("mine"), 0o600)
	r.m.trash = func(string) (trash.Entry, error) { return trash.Entry{}, trash.ErrNoTrash }
	_, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/f.bin"}, Dir: r.dir})
	if !errors.Is(err, trash.ErrNoTrash) {
		t.Fatalf("want the trash error, got %v", err)
	}
	if js, _ := r.st.ListJobs(bg, store.Filter{IncludeHistory: true}); len(js) != 1 || len(r.e.adds) != 1 {
		t.Fatalf("%d jobs, %d engine adds after a failed trash", len(js), len(r.e.adds))
	}
	if b, _ := os.ReadFile(filepath.Join(r.dir, "f.bin")); string(b) != "mine" {
		t.Fatal("the existing file changed")
	}
}
