package daemon

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// A remove that keeps files leaves an unfinished download in the download
// folder under its own name (the next free one if taken), with its control
// file, and no staging folder; undo puts it back to resume.
func TestRemoveKeepsUnfinishedInDownloadFolder(t *testing.T) {
	r := newRig(t)
	a, part := activeStaged(t, r)
	wfile(t, filepath.Join(r.dir, "a.iso"), "the user's")
	b := r.add(t, AddParams{URIs: []string{"http://example.com/b.iso"}, Out: "b.iso"})
	stageFiles(t, b, map[string]string{"b.iso": "bb", "b.iso.aria2": "ctl"})

	tok, _, err := r.m.Remove(bg, []string{a.ID, b.ID}, false)
	if err != nil || tok == "" {
		t.Fatalf("remove: %q %v", tok, err)
	}
	want := []string{"a.1.iso", "a.1.iso.aria2", "a.iso", "b.iso", "b.iso.aria2"}
	if got := listDir(t, r.dir); !slices.Equal(got, want) {
		t.Fatalf("download folder holds %v, want %v", got, want)
	}
	if read(t, filepath.Join(r.dir, "a.1.iso")) != "half" || read(t, filepath.Join(r.dir, "a.iso")) != "the user's" {
		t.Fatal("wrong bytes after the move")
	}

	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if got := listDir(t, r.dir); !slices.Equal(got, []string{".laria", "a.iso"}) {
		t.Fatalf("after undo the download folder holds %v", got)
	}
	if read(t, part) != "half" || !exists1(part+".aria2") || read(t, filepath.Join(stagingDir(b), "b.iso")) != "bb" {
		t.Fatal("undo did not bring the partials back")
	}
	if err := r.m.Resume(bg, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if add := r.e.adds[len(r.e.adds)-1]; add.Dir != stagingDir(a) || add.Out != "a.iso" {
		t.Fatalf("resume after undo: %+v", add)
	}
	if got := r.get(t, a.ID); got.State != store.Queued {
		t.Fatalf("state %s", got.State)
	}
}
