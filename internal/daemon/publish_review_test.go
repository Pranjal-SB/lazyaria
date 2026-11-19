package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// A failed copy (no space, an I/O error) must not leave anything that lets
// a later file at the name pass for our copy: the resume publishes ours
// beside it and never deletes our staged file.
func TestPublishCopyFailureThenSameSizeFile(t *testing.T) {
	for _, c := range []struct {
		name string
		fail func(*rig)
	}{
		{"no space", func(r *rig) { r.m.free = func(string) (uint64, error) { return 1, nil } }},
		{"io error", func(r *rig) {
			publishCopy = func(_, tmp string) error { return &os.PathError{Op: "write", Path: tmp, Err: syscall.EIO} }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			crossDevice(t)
			orig := publishCopy
			t.Cleanup(func() { publishCopy = orig })
			j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
			stageFiles(t, j, map[string]string{"a.iso": "ours"})
			c.fail(r)
			got := r.complete(t, j, "a.iso")
			if got.State != store.Error || got.Publish == nil || got.Publish.Temp != "" {
				t.Fatalf("after the failed copy: state %s publish %+v", got.State, got.Publish)
			}
			wfile(t, filepath.Join(r.dir, "a.iso"), "user") // same size, other bytes
			r.m.free = func(string) (uint64, error) { return 1 << 50, nil }
			publishCopy = orig
			if err := r.m.Resume(bg, []string{j.ID}); err != nil {
				t.Fatal(err)
			}
			got = r.get(t, j.ID)
			if got.State != store.Done || got.Name != "a.1.iso" {
				t.Fatalf("state %s name %q", got.State, got.Name)
			}
			if read(t, filepath.Join(r.dir, "a.iso")) != "user" || read(t, filepath.Join(r.dir, "a.1.iso")) != "ours" {
				t.Fatalf("download folder holds %v", listDir(t, r.dir))
			}
		})
	}
}

// A folder entry: any folder at the name has size 0, which must not make
// it pass for our copy.
func TestPublishFolderCopyFailureThenUserFolder(t *testing.T) {
	r := newRig(t)
	crossDevice(t)
	r.m.free = func(string) (uint64, error) { return 1, nil }
	j := r.add(t, AddParams{URIs: []string{"http://example.com/m.meta4"}})
	stageFiles(t, j, map[string]string{"sub/b.bin": "BBBB"})
	if got := r.complete(t, j, "sub/b.bin"); got.State != store.Error {
		t.Fatalf("setup: state %s", got.State)
	}
	wfile(t, filepath.Join(r.dir, "sub", "mine.txt"), "user")
	r.m.free = func(string) (uint64, error) { return 1 << 50, nil }
	if err := r.m.Resume(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	if got := r.get(t, j.ID); got.State != store.Done || got.Name != "sub.1" {
		t.Fatalf("state %s name %q", got.State, got.Name)
	}
	if names := listDir(t, filepath.Join(r.dir, "sub")); !slices.Equal(names, []string{"mine.txt"}) {
		t.Fatalf("the user's sub holds %v", names)
	}
	if read(t, filepath.Join(r.dir, "sub.1", "b.bin")) != "BBBB" {
		t.Fatal("ours not at sub.1")
	}
}

// The staging folder is not there when the publish resumes (its drive is
// not mounted yet): the job waits in dir-missing with its intent, and
// publishes once the folder is back.
func TestPublishStagingMissingKeepsIntent(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	stageFiles(t, j, map[string]string{"a.iso": "data"})
	stopAt(t, "intent")
	r.complete(t, j, "a.iso")
	publishStop = func(string) bool { return false }
	aside := filepath.Join(t.TempDir(), "unmounted")
	if err := os.Rename(filepath.Join(r.dir, ".laria"), aside); err != nil {
		t.Fatal(err)
	}
	r.m.Reconcile(bg)
	got := r.get(t, j.ID)
	if got.State != store.Error || got.ErrorKind == nil || *got.ErrorKind != "dir-missing" || got.Publish == nil || !got.Staged {
		t.Fatalf("state %s kind %v publish %+v staged %v", got.State, got.ErrorKind, got.Publish, got.Staged)
	}
	if names := listDir(t, r.dir); len(names) != 0 {
		t.Fatalf("download folder holds %v", names)
	}
	if err := os.Rename(aside, filepath.Join(r.dir, ".laria")); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Resume(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	if got := r.get(t, j.ID); got.State != store.Done || read(t, filepath.Join(r.dir, "a.iso")) != "data" || exists1(stagingDir(j)) {
		t.Fatalf("after the folder came back: state %s, download folder %v", got.State, listDir(t, r.dir))
	}
}

// After a reboot the device number may differ (btrfs, overlay); a publish
// whose first entry is already in Dir is not taken for a missing drive.
func TestPublishResumesAfterDeviceChange(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/m.meta4"}})
	stageFiles(t, j, map[string]string{"a.bin": "A", "b.bin": "B"})
	r.e.set(r.ref(t, j.ID), func(s *engine.EngineStatus) { s.State = "active" })
	r.event(r.ref(t, j.ID), engine.KindStart) // started, so the device check applies
	stopAt(t, "moved")
	r.complete(t, j, "a.bin")
	publishStop = func(string) bool { return false }
	r.m.dev = func(string) (uint64, error) { return 99, nil }
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if got := r.get(t, j.ID); got.State != store.Done {
		t.Fatalf("state %s kind %v", got.State, got.ErrorKind)
	}
	if names := listDir(t, r.dir); !slices.Equal(names, []string{"a.bin", "b.bin"}) {
		t.Fatalf("download folder holds %v", names)
	}
}

// The same link added twice: job B publishes a.iso first; job A, resumed
// after a failed copy, must not adopt B's file.
func TestPublishNeverAdoptsAnotherJobsFile(t *testing.T) {
	r := newRig(t)
	crossDevice(t)
	a := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Force: true})
	b := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Force: true})
	stageFiles(t, a, map[string]string{"a.iso": "AAAA"})
	stageFiles(t, b, map[string]string{"a.iso": "BBBB"})
	r.m.free = func(string) (uint64, error) { return 1, nil }
	r.complete(t, a, "a.iso")
	r.m.free = func(string) (uint64, error) { return 1 << 50, nil }
	if got := r.complete(t, b, "a.iso"); got.State != store.Done || got.Name != "a.iso" {
		t.Fatalf("job B: %s %q", got.State, got.Name)
	}
	if err := r.m.Resume(bg, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if got := r.get(t, a.ID); got.State != store.Done || got.Name != "a.1.iso" {
		t.Fatalf("job A: %s %q", got.State, got.Name)
	}
	if read(t, filepath.Join(r.dir, "a.iso")) != "BBBB" || read(t, filepath.Join(r.dir, "a.1.iso")) != "AAAA" {
		t.Fatalf("download folder holds %v", listDir(t, r.dir))
	}
}

// A finished job's staging folder left behind (a crash after the done
// update, or a failed removal) goes to the trash on the next Reconcile.
func TestReconcileTrashesFinishedJobsStaging(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	r.m.s.Load().Downloads.Dir = r.dir
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	stageFiles(t, j, map[string]string{"a.iso": "data"})
	if got := r.complete(t, j, "a.iso"); got.State != store.Done {
		t.Fatal(got.State)
	}
	wfile(t, filepath.Join(stagingDir(j), "a.iso.leftover"), "big copy")
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if exists1(stagingDir(j)) || read(t, filepath.Join(r.dir, "a.iso")) != "data" {
		t.Fatalf("staging there %v, download folder %v", exists1(stagingDir(j)), listDir(t, r.dir))
	}
}

// A metalink publishes several entries; each is the job's, for the origin
// stamp and for remove --delete-files, not only the first.
func TestPublishedEntriesAllTracked(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	r.m.s.Load().Privacy.StampOriginXattr = true
	j := r.add(t, AddParams{URIs: []string{"http://example.com/m.meta4"}})
	stageFiles(t, j, map[string]string{"a.bin": "A", "b.bin": "B"})
	if got := r.complete(t, j, "a.bin"); got.State != store.Done {
		t.Fatalf("state %s", got.State)
	}
	a, b := filepath.Join(r.dir, "a.bin"), filepath.Join(r.dir, "b.bin")
	if err := unix.Setxattr(a, "user.probe", []byte("1"), 0); err == nil {
		if origin(a) == "" || origin(b) == "" {
			t.Fatalf("stamped: a %q b %q", origin(a), origin(b))
		}
	}
	if _, _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil {
		t.Fatal(err)
	}
	if names := listDir(t, r.dir); len(names) != 0 {
		t.Fatalf("download folder holds %v", names)
	}
}

// overlayfs refuses to rename a folder, so the copied folder is built in
// the download folder file by file. A crash halfway resumes into the same
// folder: no second copy beside it, and the half-built one is never removed.
func TestPublishFolderBuildResumes(t *testing.T) {
	r := newRig(t)
	crossDevice(t)
	origRename, origMove := publishRenameTemp, publishMoveInto
	publishRenameTemp = func(old, new string) error {
		if isDir(old) {
			return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.EXDEV}
		}
		return origRename(old, new)
	}
	publishMoveInto = func(src, dst string, resume bool) error {
		// Build only a.flac, then crash.
		if err := os.Mkdir(dst, 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(src, "a.flac"), filepath.Join(dst, "a.flac")); err != nil {
			return err
		}
		return errPublishStopped
	}
	t.Cleanup(func() { publishRenameTemp, publishMoveInto = origRename, origMove })
	j := r.add(t, AddParams{URIs: []string{"http://example.com/m.meta4"}})
	stageFiles(t, j, map[string]string{"album/a.flac": "A", "album/b.flac": "B"})
	got := r.complete(t, j, "album/a.flac")
	if got.Publish == nil || got.Publish.Building != "album" || !exists1(filepath.Join(r.dir, "album", "a.flac")) {
		t.Fatalf("after the crash: publish %+v, download folder %v", got.Publish, listDir(t, r.dir))
	}
	publishMoveInto = origMove
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if got := r.get(t, j.ID); got.State != store.Done || got.Name != "album" {
		t.Fatalf("state %s name %q", got.State, got.Name)
	}
	if names := listDir(t, r.dir); !slices.Equal(names, []string{"album"}) {
		t.Fatalf("download folder holds %v", names)
	}
	if read(t, filepath.Join(r.dir, "album", "a.flac")) != "A" || read(t, filepath.Join(r.dir, "album", "b.flac")) != "B" {
		t.Fatal("album incomplete")
	}
}
