package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/fsutil"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// The control file goes to the trash before the payload moves, so a crash
// in between never leaves a lone payload outside staging. A partial
// without one (aria2 writes none for a reply of unknown length) still
// moves under the unfinished name, and the same link can be added again.
func TestKeepUnfinishedControlFileFirst(t *testing.T) {
	r := newRig(t)
	a, _ := activeStaged(t, r)
	b := r.add(t, AddParams{URIs: []string{"http://example.com/b.iso"}, Out: "b.iso"})
	stageFiles(t, b, map[string]string{"b.iso": "chunked"})
	var order []string
	keepRename = func(from, to string) error {
		order = append(order, "move "+filepath.Base(to))
		return fsutil.RenameNoReplace(from, to)
	}
	keepTrash = func(p, shown string) (trash.Entry, error) {
		order = append(order, "trash "+filepath.Base(shown))
		return trash.TrashAs(p, shown)
	}
	t.Cleanup(func() { keepRename, keepTrash = fsutil.RenameNoReplace, trash.TrashAs })

	if _, _, err := r.m.Remove(bg, []string{a.ID, b.ID}, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"trash a (unfinished).iso.aria2", "move a (unfinished).iso", "move b (unfinished).iso"}; !slices.Equal(order, want) {
		t.Fatalf("did %v, want %v", order, want)
	}
	if read(t, filepath.Join(r.dir, "b (unfinished).iso")) != "chunked" {
		t.Fatal("the partial without a control file was not kept")
	}
	if _, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/b.iso"}, Dir: r.dir}); err != nil {
		t.Fatalf("adding the link again: %v", err)
	}
}

// Undo only takes back the files the remove moved: another file now at
// the kept name stays where it is.
func TestUndoLeavesAForeignFileAtTheKeptName(t *testing.T) {
	r := newRig(t)
	a, part := activeStaged(t, r)
	tok, _, err := r.m.Remove(bg, []string{a.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(r.dir, "a (unfinished).iso")
	// Made before the kept file goes, so it cannot reuse its inode.
	other := filepath.Join(t.TempDir(), "other")
	wfile(t, other, "someone else's")
	if err := os.Rename(other, kept); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if read(t, kept) != "someone else's" || exists1(part) {
		t.Fatal("undo moved a file the remove did not move")
	}
	if !exists1(part + ".aria2") {
		t.Fatal("the control file the remove trashed did not come back")
	}
}

// Without a usable trash the control file stays beside the partial, as
// before, rather than being deleted.
func TestKeepUnfinishedNoTrashKeepsControlFileBeside(t *testing.T) {
	r := newRig(t)
	a, part := activeStaged(t, r)
	keepTrash = func(string, string) (trash.Entry, error) { return trash.Entry{}, trash.ErrNoTrash }
	t.Cleanup(func() { keepTrash = trash.TrashAs })
	tok, _, err := r.m.Remove(bg, []string{a.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := listDir(t, r.dir); !slices.Equal(got, []string{"a (unfinished).iso", "a (unfinished).iso.aria2"}) {
		t.Fatalf("download folder holds %v", got)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if read(t, part) != "half" || read(t, part+".aria2") != "ctl" {
		t.Fatal("undo did not bring the partial and its control file back")
	}
}

// What the move leaves in the staging folder is never hard-deleted, except
// aria2's own <info-hash>.torrent: the rest (here a file that happens to
// end in .meta4) goes to the trash, and undo brings it back.
func TestKeepUnfinishedTrashesWhatIsLeft(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	a, part := activeStaged(t, r)
	hash := "0123456789abcdef0123456789abcdef01234567.torrent"
	stageFiles(t, a, map[string]string{"notes.meta4": "the user's", hash: "meta"})

	tok, _, err := r.m.Remove(bg, []string{a.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	if exists1(stagingDir(a)) {
		t.Fatal("staging folder left")
	}
	// What is left in the staging folder, then the control file: undo
	// restores the folder before putting the control file into it.
	es := r.m.undo[tok].files[a.ID]
	if len(es) != 2 || es[0].Original != stagingDir(a) || read(t, filepath.Join(es[0].TrashedPath, "notes.meta4")) != "the user's" || exists1(filepath.Join(es[0].TrashedPath, hash)) {
		t.Fatalf("trashed %+v", es)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if read(t, part) != "half" || read(t, filepath.Join(stagingDir(a), "notes.meta4")) != "the user's" {
		t.Fatal("undo did not bring everything back")
	}
}

// A remove that keeps files leaves an unfinished download in the download
// folder as "<stem> (unfinished)<ext>" (the next free name if taken), never
// under its plain name, and no staging folder. Its control file goes to
// the trash under the matching name; undo puts both back to resume.
func TestRemoveKeepsUnfinishedInDownloadFolder(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	a, part := activeStaged(t, r)
	wfile(t, filepath.Join(r.dir, "a.iso"), "the user's")
	wfile(t, filepath.Join(r.dir, "a (unfinished).iso"), "an older one")
	b := r.add(t, AddParams{URIs: []string{"http://example.com/b.iso"}, Out: "b.iso"})
	stageFiles(t, b, map[string]string{"b.iso": "bb", "b.iso.aria2": "ctl"})

	tok, _, err := r.m.Remove(bg, []string{a.ID, b.ID}, false)
	if err != nil || tok == "" {
		t.Fatalf("remove: %q %v", tok, err)
	}
	want := []string{"a (unfinished).1.iso", "a (unfinished).iso", "a.iso", "b (unfinished).iso"}
	if got := listDir(t, r.dir); !slices.Equal(got, want) {
		t.Fatalf("download folder holds %v, want %v", got, want)
	}
	trashed := listDir(t, filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "files"))
	if !slices.Equal(trashed, []string{"a (unfinished).1.iso.aria2", "b (unfinished).iso.aria2"}) {
		t.Fatalf("trash holds %v", trashed)
	}
	if read(t, filepath.Join(r.dir, "a (unfinished).1.iso")) != "half" || read(t, filepath.Join(r.dir, "a.iso")) != "the user's" {
		t.Fatal("wrong bytes after the move")
	}

	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if got := listDir(t, r.dir); !slices.Equal(got, []string{".laria", "a (unfinished).iso", "a.iso"}) {
		t.Fatalf("after undo the download folder holds %v", got)
	}
	if read(t, part) != "half" || !exists1(part+".aria2") || read(t, filepath.Join(stagingDir(b), "b.iso")) != "bb" || !exists1(filepath.Join(stagingDir(b), "b.iso.aria2")) {
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
