package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/fsutil"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// stageFiles writes name -> content into j's staging folder.
func stageFiles(t *testing.T, j *store.Job, files map[string]string) {
	t.Helper()
	for n, s := range files {
		wfile(t, filepath.Join(stagingDir(j), n), s)
	}
}

// complete reports j's download complete with path (relative to staging).
func (r *rig) complete(t *testing.T, j *store.Job, path string) *store.Job {
	t.Helper()
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) {
		s.State = "complete"
		if path != "" {
			s.Path = filepath.Join(stagingDir(j), path)
		}
	})
	r.event(ref, engine.KindComplete)
	return r.get(t, j.ID)
}

// listDir is the sorted entry names in dir.
func listDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stopAt makes publish stop after step, as a crash would; restored on cleanup.
func stopAt(t *testing.T, step string) {
	t.Helper()
	orig := publishStop
	publishStop = func(s string) bool { return s == step }
	t.Cleanup(func() { publishStop = orig })
}

func TestPublishMovesFileUp(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	stageFiles(t, j, map[string]string{"a.iso": "data"})
	got := r.complete(t, j, "a.iso")
	if got.State != store.Done || got.Staged || publishing(got) || got.Out == nil || *got.Out != "a.iso" || got.Name != "a.iso" || got.FinishedAt == nil {
		t.Fatalf("job: state %s staged %v publish %+v out %v name %q", got.State, got.Staged, got.Publish, got.Out, got.Name)
	}
	if read(t, filepath.Join(r.dir, "a.iso")) != "data" {
		t.Fatal("bytes changed")
	}
	if exists1(stagingDir(j)) {
		t.Fatal("staging folder left behind")
	}
	if names := listDir(t, r.dir); !slices.Equal(names, []string{"a.iso"}) {
		t.Fatalf("download folder holds %v", names)
	}
	if !r.hasEvent(t, j.ID, "published") {
		t.Fatal("no published event")
	}
}

func TestPublishPicksFreeNameAtTheEnd(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	user := filepath.Join(r.dir, "a.iso")
	wfile(t, user, "the user's")
	ino, _, _, _ := fsutil.FileID(user)
	stageFiles(t, j, map[string]string{"a.iso": "ours"})
	got := r.complete(t, j, "a.iso")
	if got.State != store.Done || got.Name != "a.1.iso" || *got.Out != "a.1.iso" {
		t.Fatalf("state %s name %q", got.State, got.Name)
	}
	if read(t, user) != "the user's" {
		t.Fatal("the user's file changed")
	}
	if ino2, _, _, _ := fsutil.FileID(user); ino2 != ino {
		t.Fatal("the user's file was replaced")
	}
	if read(t, filepath.Join(r.dir, "a.1.iso")) != "ours" {
		t.Fatal("ours not at a.1.iso")
	}
}

// A server may name a file .bashrc; it must not land hidden. A name the
// user gives is cleaned the same way when added, so it never starts with a dot.
func TestPublishStripsServerDot(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/"}})
	stageFiles(t, j, map[string]string{".bashrc": "x"})
	if got := r.complete(t, j, ".bashrc"); got.State != store.Done || got.Name != "bashrc" {
		t.Fatalf("server name: state %s name %q", got.State, got.Name)
	}
	if !exists1(filepath.Join(r.dir, "bashrc")) || exists1(filepath.Join(r.dir, ".bashrc")) {
		t.Fatalf("download folder holds %v", listDir(t, r.dir))
	}

	k := r.add(t, AddParams{URIs: []string{"http://example.com/h"}, Out: ".hidden"})
	if k.Out == nil || *k.Out != "hidden" {
		t.Fatalf("user out stored as %v, want it cleaned at add", k.Out)
	}
	stageFiles(t, k, map[string]string{"hidden": "y"})
	if got := r.complete(t, k, "hidden"); got.Name != "hidden" || !exists1(filepath.Join(r.dir, "hidden")) {
		t.Fatalf("user out: name %q", got.Name)
	}
}

func TestPublishSkipsControlFiles(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/x.bin"}})
	stageFiles(t, j, map[string]string{"x.bin": "x", "x.bin.aria2": "c", "abc.torrent": "t", "m.meta4": "m"})
	got := r.complete(t, j, "x.bin")
	if got.State != store.Done || publishing(got) {
		t.Fatalf("state %s publish %+v", got.State, got.Publish)
	}
	if names := listDir(t, r.dir); !slices.Equal(names, []string{"x.bin"}) {
		t.Fatalf("download folder holds %v", names)
	}
}

func TestPublishEmptyIsError(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/m.meta4"}})
	stageFiles(t, j, map[string]string{"m.meta4": "m"})
	got := r.complete(t, j, "m.meta4")
	want := "the download finished but produced no files (a metalink with invalid names does this); nothing was moved"
	if got.State != store.Error || got.ErrorKind == nil || *got.ErrorKind != "empty" || got.ErrorMsg == nil || *got.ErrorMsg != want {
		t.Fatalf("state %s kind %v msg %v", got.State, got.ErrorKind, got.ErrorMsg)
	}
	if names := listDir(t, r.dir); !slices.Equal(names, []string{".laria"}) {
		t.Fatalf("download folder holds %v", names)
	}
	if !exists1(filepath.Join(stagingDir(j), "m.meta4")) {
		t.Fatal("staging changed")
	}
}

func TestPublishResumesAfterCrash(t *testing.T) {
	for _, step := range []string{"intent", "moved"} {
		t.Run(step, func(t *testing.T) {
			r := newRig(t)
			j := r.add(t, AddParams{URIs: []string{"http://example.com/m.meta4"}})
			stageFiles(t, j, map[string]string{"a.bin": "A", "b.bin": "B"})
			stopAt(t, step)
			got := r.complete(t, j, "a.bin")
			if got.State == store.Done || got.Publish == nil || len(got.Publish.Entries) != 2 {
				t.Fatalf("after the crash: state %s publish %+v", got.State, got.Publish)
			}
			publishStop = func(string) bool { return false }
			if err := r.m.Reconcile(bg); err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				got := r.get(t, j.ID)
				if got.State != store.Done || publishing(got) || got.Staged || got.Name != "a.bin" {
					t.Fatalf("state %s publish %+v name %q", got.State, got.Publish, got.Name)
				}
				if names := listDir(t, r.dir); !slices.Equal(names, []string{"a.bin", "b.bin"}) {
					t.Fatalf("download folder holds %v", names)
				}
				if read(t, filepath.Join(r.dir, "a.bin")) != "A" || read(t, filepath.Join(r.dir, "b.bin")) != "B" {
					t.Fatal("bytes changed")
				}
			}
			check()
			if err := r.m.Reconcile(bg); err != nil {
				t.Fatal(err)
			}
			check()
		})
	}
}

// crossDevice makes every publish rename fail with EXDEV.
func crossDevice(t *testing.T) {
	t.Helper()
	orig := publishRename
	publishRename = func(old, new string) error {
		return &os.LinkError{Op: "rename", Old: old, New: new, Err: syscall.EXDEV}
	}
	t.Cleanup(func() { publishRename = orig })
}

func TestPublishCrossDevice(t *testing.T) {
	t.Run("copy", func(t *testing.T) {
		r := newRig(t)
		crossDevice(t)
		j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
		stageFiles(t, j, map[string]string{"a.iso": "data"})
		src := filepath.Join(stagingDir(j), "a.iso")
		copies := 0
		orig := publishCopy
		publishCopy = func(s, tmp string) error {
			copies++
			if p := r.get(t, j.ID).Publish; p == nil || p.Temp == "" || filepath.Join(r.dir, p.Temp) != tmp {
				t.Errorf("temp not recorded before the copy: %+v, copying through %s", p, tmp)
			}
			err := orig(s, tmp)
			if !exists1(src) {
				t.Error("source removed during the copy")
			}
			return err
		}
		t.Cleanup(func() { publishCopy = orig })
		got := r.complete(t, j, "a.iso")
		if copies != 1 || got.State != store.Done || publishing(got) {
			t.Fatalf("copies %d state %s publish %+v", copies, got.State, got.Publish)
		}
		if names := listDir(t, r.dir); !slices.Equal(names, []string{"a.iso"}) || read(t, filepath.Join(r.dir, "a.iso")) != "data" {
			t.Fatalf("download folder holds %v", names)
		}
		if exists1(stagingDir(j)) {
			t.Fatal("staging left behind")
		}
	})
	// Crashes after the copy and during it (a temp left over) both end with
	// one complete copy and no temp.
	for _, step := range []string{"copied", "copy-temp"} {
		t.Run("crash-"+step, func(t *testing.T) {
			r := newRig(t)
			crossDevice(t)
			j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
			stageFiles(t, j, map[string]string{"a.iso": "data"})
			stopAt(t, step)
			got := r.complete(t, j, "a.iso")
			if got.Publish == nil || got.Publish.Temp == "" {
				t.Fatalf("after the crash: publish %+v", got.Publish)
			}
			var trashed []string
			r.m.trash = func(p string) (trash.Entry, error) {
				trashed = append(trashed, p)
				return trash.Trash(p)
			}
			t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
			var temp string
			if step == "copy-temp" {
				temp = filepath.Join(r.dir, got.Publish.Temp)
				wfile(t, temp, "half a co")
			}
			publishStop = func(string) bool { return false }
			if err := r.m.Reconcile(bg); err != nil {
				t.Fatal(err)
			}
			if temp != "" && !slices.Equal(trashed, []string{temp}) {
				t.Fatalf("trashed %v; want the unfinished temp in the trash", trashed)
			}
			got = r.get(t, j.ID)
			if got.State != store.Done || publishing(got) || got.Name != "a.iso" {
				t.Fatalf("state %s publish %+v name %q", got.State, got.Publish, got.Name)
			}
			if names := listDir(t, r.dir); !slices.Equal(names, []string{"a.iso"}) || read(t, filepath.Join(r.dir, "a.iso")) != "data" {
				t.Fatalf("download folder holds %v", names)
			}
		})
	}
}

// A copy refused because something took the temp name is not ours: the
// name is not kept, and that file is left alone.
func TestPublishTempNameTakenIsNotKept(t *testing.T) {
	r := newRig(t)
	crossDevice(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	stageFiles(t, j, map[string]string{"a.iso": "data"})
	var temp string
	orig := publishCopy
	publishCopy = func(s, tmp string) error {
		temp = tmp
		wfile(t, tmp, "someone else's")
		return orig(s, tmp)
	}
	t.Cleanup(func() { publishCopy = orig })
	got := r.complete(t, j, "a.iso")
	if got.State != store.Error || got.Publish == nil || got.Publish.Temp != "" {
		t.Fatalf("state %s publish %+v", got.State, got.Publish)
	}
	if read(t, temp) != "someone else's" {
		t.Fatal("the file at the temp name changed")
	}
}

func TestReconcileTrashesOrphanStaging(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	r.m.s.Load().Downloads.Dir = r.dir
	live := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	stageFiles(t, live, map[string]string{"a.iso": "partial"})
	// Only folders older than an hour: a fresh one may belong to another
	// lazyaria sharing the download folder.
	orphan := filepath.Join(r.dir, ".laria", store.NewID())
	wfile(t, filepath.Join(orphan, "f"), "left by a crash")
	old := r.clk.now().Add(-2 * time.Hour)
	os.Chtimes(orphan, old, old)
	fresh := filepath.Join(r.dir, ".laria", store.NewID())
	wfile(t, filepath.Join(fresh, "f"), "another instance's")
	os.Chtimes(fresh, r.clk.now(), r.clk.now())
	var trashed []string
	r.m.trash = func(p string) (trash.Entry, error) {
		trashed = append(trashed, p)
		return trash.Trash(p)
	}
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(trashed, []string{orphan}) || exists1(orphan) {
		t.Fatalf("trashed %v, orphan still there %v", trashed, exists1(orphan))
	}
	if read(t, filepath.Join(stagingDir(live), "a.iso")) != "partial" {
		t.Fatal("a live job's staging folder was touched")
	}
	if !exists1(fresh) {
		t.Fatal("a fresh folder with no job was trashed")
	}
}
