package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// activeStaged adds a.iso, starts it and gives it a partial with its control file.
func activeStaged(t *testing.T, r *rig) (*store.Job, string) {
	t.Helper()
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	ref := r.ref(t, j.ID)
	part := filepath.Join(stagingDir(j), "a.iso")
	stageFiles(t, j, map[string]string{"a.iso": "half", "a.iso.aria2": "ctl"})
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.Path, s.Done, s.Total = "active", part, 4, 100 })
	r.event(ref, engine.KindStart)
	return r.get(t, j.ID), part
}

func TestRemoveStagedTrashesStagingOnly(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j, part := activeStaged(t, r)
	user := filepath.Join(r.dir, "a.iso")
	wfile(t, user, "the user's")
	tok, _, err := r.m.Remove(bg, []string{j.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	es := r.m.undo[tok].files[j.ID]
	if len(es) != 1 || es[0].Original != stagingDir(j) || exists1(stagingDir(j)) {
		t.Fatalf("trashed %+v, staging still there %v", es, exists1(stagingDir(j)))
	}
	if read(t, user) != "the user's" {
		t.Fatal("the user's a.iso was touched")
	}
	if names := listDir(t, r.dir); !slices.Equal(names, []string{"a.iso"}) {
		t.Fatalf("download folder holds %v", names)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if read(t, part) != "half" || !exists1(part+".aria2") {
		t.Fatal("undo did not bring the staging folder back")
	}
	if err := r.m.Resume(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	add := r.e.adds[len(r.e.adds)-1]
	if add.Dir != stagingDir(j) || add.Out != "a.iso" || read(t, part) != "half" {
		t.Fatalf("resume after undo: %+v", add)
	}
}

func TestReaddWithoutControlFileEmptiesStaging(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.bin"}, Out: "a.bin"})
	user := filepath.Join(r.dir, "a.bin")
	wfile(t, user, "the user's")
	stageFiles(t, j, map[string]string{"a.bin": "stale partial", "x.torrent": "t"})
	j = r.m.apply(bg, j, engine.EngineStatus{State: "error", ErrorCode: 1, Done: 13, Path: filepath.Join(stagingDir(j), "a.bin")}, "")
	if _, err := r.m.readd(bg, r.get(t, j.ID), false, "resumed"); err != nil {
		t.Fatal(err)
	}
	if names := listDir(t, stagingDir(j)); len(names) != 0 {
		t.Fatalf("staging still holds %v", names)
	}
	add := r.e.adds[len(r.e.adds)-1]
	if add.Dir != stagingDir(j) || add.Out != "a.bin" {
		t.Fatalf("re-add %+v; want it in staging", add)
	}
	if read(t, user) != "the user's" {
		t.Fatal("a file outside staging was touched")
	}
}

func TestCompletedOfflineStaged(t *testing.T) {
	t.Run("full size", func(t *testing.T) {
		r := newRig(t)
		total := int64(5)
		out := "full.bin"
		j := r.insert(t, store.Active, "old1", func(j *store.Job) { j.Out, j.Name, j.TotalBytes = &out, out, &total })
		stageFiles(t, j, map[string]string{"full.bin": "12345"})
		if err := r.m.Reconcile(bg); err != nil {
			t.Fatal(err)
		}
		got := r.get(t, j.ID)
		if adds, _ := r.e.count(); adds != 0 || got.State != store.Done || got.Staged || !r.hasEvent(t, j.ID, "completed-offline") {
			t.Fatalf("adds %d state %s staged %v", adds, got.State, got.Staged)
		}
		if read(t, filepath.Join(r.dir, "full.bin")) != "12345" || exists1(stagingDir(j)) {
			t.Fatal("not published")
		}
	})
	t.Run("total unknown", func(t *testing.T) {
		r := newRig(t)
		out := "u.bin"
		j := r.insert(t, store.Active, "old1", func(j *store.Job) { j.Out = &out })
		stageFiles(t, j, map[string]string{"u.bin": "12345"})
		if err := r.m.Reconcile(bg); err != nil {
			t.Fatal(err)
		}
		if adds, _ := r.e.count(); adds != 1 || r.get(t, j.ID).State != store.Queued {
			t.Fatalf("adds %d state %s", adds, r.get(t, j.ID).State)
		}
		if names := listDir(t, stagingDir(j)); len(names) != 0 {
			t.Fatalf("staging still holds %v", names)
		}
		if names := listDir(t, r.dir); !slices.Equal(names, []string{".laria"}) {
			t.Fatalf("download folder holds %v", names)
		}
	})
}

func TestRefreshPausedStagedKeepsBytes(t *testing.T) {
	r := newRig(t)
	j, part := activeStaged(t, r)
	if err := r.m.Pause(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.RefreshURI(bg, j.ID, "http://mirror.example.com/a.iso"); err != nil {
		t.Fatal(err)
	}
	if read(t, part) != "half" || !exists1(part+".aria2") {
		t.Fatal("the partial or its control file is gone")
	}
	if names := listDir(t, stagingDir(j)); !slices.Equal(names, []string{"a.iso", "a.iso.aria2"}) {
		t.Fatalf("staging holds %v", names)
	}
}

// aria2 starts a download with no control file over under a new name, even
// on unpause; the file it left is the job's own and goes.
func TestRestartInStagingDropsOldFile(t *testing.T) {
	r := newRig(t)
	j, part := activeStaged(t, r)
	os.Remove(part + ".aria2")
	ref := r.ref(t, j.ID)
	newer := filepath.Join(stagingDir(j), "a.1.iso")
	wfile(t, newer, "fresh")
	r.e.set(ref, func(s *engine.EngineStatus) { s.Path, s.Done = newer, 0 })
	r.m.poll(bg)
	if got := r.get(t, j.ID); got.Out == nil || *got.Out != "a.1.iso" {
		t.Fatalf("out %v", got.Out)
	}
	if names := listDir(t, stagingDir(j)); !slices.Equal(names, []string{"a.1.iso"}) {
		t.Fatalf("staging holds %v", names)
	}
}

// A remove while a publish is recorded finishes the publish first, so no
// file is left in the download folder that no job accounts for.
func TestRemoveDuringPublish(t *testing.T) {
	t.Run("finishes then trashes", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
		r := newRig(t)
		j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
		stageFiles(t, j, map[string]string{"a.iso": "data"})
		stopAt(t, "intent")
		r.complete(t, j, "a.iso")
		publishStop = func(string) bool { return false }
		tok, _, err := r.m.Remove(bg, []string{j.ID}, true)
		if err != nil {
			t.Fatal(err)
		}
		if names := listDir(t, r.dir); len(names) != 0 {
			t.Fatalf("download folder holds %v", names)
		}
		if err := r.m.Undo(bg, tok); err != nil {
			t.Fatal(err)
		}
		if got := r.get(t, j.ID); got.State != store.Done || read(t, filepath.Join(r.dir, "a.iso")) != "data" {
			t.Fatalf("after undo: state %s", got.State)
		}
	})
	t.Run("keeps files", func(t *testing.T) {
		r := newRig(t)
		j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
		stageFiles(t, j, map[string]string{"a.iso": "data"})
		stopAt(t, "intent")
		r.complete(t, j, "a.iso")
		publishStop = func(string) bool { return false }
		if _, _, err := r.m.Remove(bg, []string{j.ID}, false); err != nil {
			t.Fatal(err)
		}
		got := r.get(t, j.ID)
		if got.State != store.Removed || got.Staged || publishing(got) || read(t, filepath.Join(r.dir, "a.iso")) != "data" || exists1(stagingDir(j)) {
			t.Fatalf("state %s staged %v publish %+v", got.State, got.Staged, got.Publish)
		}
	})
	t.Run("publish fails", func(t *testing.T) {
		// The second entry cannot move: the first is already in the
		// download folder, and remove-with-files takes it too.
		t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
		r := newRig(t)
		j := r.add(t, AddParams{URIs: []string{"http://example.com/m.meta4"}})
		stageFiles(t, j, map[string]string{"a.bin": "A", "b.bin": "B"})
		orig := publishRename
		publishRename = func(old, new string) error {
			if filepath.Base(old) == "b.bin" {
				return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.EACCES}
			}
			return orig(old, new)
		}
		t.Cleanup(func() { publishRename = orig })
		got := r.complete(t, j, "a.bin")
		if got.State != store.Error || got.Publish == nil || !exists1(filepath.Join(r.dir, "a.bin")) {
			t.Fatalf("setup: state %s publish %+v", got.State, got.Publish)
		}
		if _, _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil {
			t.Fatal(err)
		}
		if names := listDir(t, r.dir); len(names) != 0 {
			t.Fatalf("download folder holds %v", names)
		}
	})
}

// A metalink with several files runs several engine downloads in one
// staging folder; the job follows the first. Nothing is published while
// another one still writes there, and remove stops them all.
func TestStagingWaitsForSiblingDownloads(t *testing.T) {
	setup := func(t *testing.T) (*rig, *store.Job) {
		r := newRig(t)
		j := r.add(t, AddParams{URIs: []string{"http://example.com/m.meta4"}})
		stageFiles(t, j, map[string]string{"a.bin": "A", "b.bin": "B-part", "b.bin.aria2": "ctl"})
		r.e.set("sibling", func(s *engine.EngineStatus) { s.State, s.Path = "active", filepath.Join(stagingDir(j), "b.bin") })
		return r, j
	}
	t.Run("publish waits", func(t *testing.T) {
		r, j := setup(t)
		got := r.complete(t, j, "a.bin")
		if got.State == store.Done || got.Publish != nil || !exists1(filepath.Join(stagingDir(j), "b.bin")) {
			t.Fatalf("published while a sibling writes: state %s publish %+v", got.State, got.Publish)
		}
		if names := listDir(t, r.dir); !slices.Equal(names, []string{".laria"}) {
			t.Fatalf("download folder holds %v", names)
		}
		wfile(t, filepath.Join(stagingDir(j), "b.bin"), "B")
		os.Remove(filepath.Join(stagingDir(j), "b.bin.aria2"))
		r.e.setState("sibling", "complete")
		r.m.poll(bg)
		if got := r.get(t, j.ID); got.State != store.Done {
			t.Fatalf("state %s after the sibling finished", got.State)
		}
		if names := listDir(t, r.dir); !slices.Equal(names, []string{"a.bin", "b.bin"}) {
			t.Fatalf("download folder holds %v", names)
		}
	})
	t.Run("remove stops them", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
		r, j := setup(t)
		if _, _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(r.e.removed, engine.EngineRef("sibling")) || exists1(stagingDir(j)) {
			t.Fatalf("removed %v, staging there %v", r.e.removed, exists1(stagingDir(j)))
		}
	})
}

var _ = errors.New
