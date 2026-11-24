package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// linkTo is where the symlink p points, or "" when p is not a symlink.
func linkTo(t *testing.T, p string) string {
	t.Helper()
	s, err := os.Readlink(p)
	if err != nil {
		return ""
	}
	return s
}

func markerOf(j *store.Job, out string) string { return filepath.Join(".laria", j.ID, out) }

// startStaged adds name and reports it downloading into its staging folder.
func startStaged(t *testing.T, r *rig, name string) *store.Job {
	t.Helper()
	j := r.add(t, AddParams{URIs: []string{"http://example.com/" + name}, Out: name})
	stageFiles(t, j, map[string]string{name: "half"})
	r.e.set(r.ref(t, j.ID), func(s *engine.EngineStatus) {
		s.State, s.Path = "active", filepath.Join(stagingDir(j), name)
	})
	r.m.poll(bg)
	return r.get(t, j.ID)
}

func TestMarkerCreatedAndRepointed(t *testing.T) {
	r := newRig(t)
	j, _ := activeStaged(t, r)
	marker := filepath.Join(r.dir, "a.iso.laria")
	if got := linkTo(t, marker); got != markerOf(j, "a.iso") {
		t.Fatalf("marker points to %q", got)
	}
	if read(t, marker) != "half" {
		t.Fatal("marker does not reach the partial")
	}
	// A marker of ours left pointing at an old file is re-pointed.
	os.Remove(marker)
	if err := os.Symlink(markerOf(j, "gone.iso"), marker); err != nil {
		t.Fatal(err)
	}
	r.m.poll(bg)
	if got := linkTo(t, marker); got != markerOf(j, "a.iso") {
		t.Fatalf("stale marker points to %q", got)
	}
	// aria2 starts over beside its file (a server without ranges): the
	// marker follows the new name.
	os.Remove(filepath.Join(stagingDir(j), "a.iso.aria2"))
	newer := filepath.Join(stagingDir(j), "a.1.iso")
	wfile(t, newer, "fresh")
	r.e.set(r.ref(t, j.ID), func(s *engine.EngineStatus) { s.Path, s.Done = newer, 0 })
	r.m.poll(bg)
	if lexists(marker) {
		t.Fatal("the old marker is left")
	}
	if got := linkTo(t, filepath.Join(r.dir, "a.1.iso.laria")); got != markerOf(j, "a.1.iso") {
		t.Fatalf("new marker points to %q", got)
	}
}

func TestMarkerSkippedWhenLinksUnsupported(t *testing.T) {
	orig := markSymlink
	markSymlink = func(string, string) error { return &os.LinkError{Op: "symlink", Err: syscall.EPERM} }
	t.Cleanup(func() { markSymlink = orig })
	r := newRig(t)
	j, _ := activeStaged(t, r)
	if got := r.get(t, j.ID); got.State != store.Active || got.ErrorKind != nil {
		t.Fatalf("state %s error %v", got.State, got.ErrorKind)
	}
	if names := listDir(t, r.dir); !slices.Equal(names, []string{".laria"}) {
		t.Fatalf("download folder holds %v", names)
	}
}

func TestMarkerNeverReplacesUserFile(t *testing.T) {
	r := newRig(t)
	mine := filepath.Join(r.dir, "a.iso.laria")
	wfile(t, mine, "the user's")
	j, _ := activeStaged(t, r)
	// Another job's marker, or any link not into this job's staging folder.
	other := filepath.Join(r.dir, "b.iso.laria")
	if err := os.Symlink(filepath.Join(".laria", "someone-else", "b.iso"), other); err != nil {
		t.Fatal(err)
	}
	k := startStaged(t, r, "b.iso")
	if read(t, mine) != "the user's" || linkTo(t, mine) != "" {
		t.Fatal("the user's a.iso.laria was replaced")
	}
	if got := linkTo(t, other); got != filepath.Join(".laria", "someone-else", "b.iso") {
		t.Fatalf("a link not ours was re-pointed to %q", got)
	}
	if _, _, err := r.m.Remove(bg, []string{j.ID, k.ID}, false); err != nil {
		t.Fatal(err)
	}
	if read(t, mine) != "the user's" || !lexists(other) {
		t.Fatal("remove took a file that is not our marker")
	}
}

func TestMarkerRemovedOnPublishAndRemove(t *testing.T) {
	r := newRig(t)
	j, _ := activeStaged(t, r)
	marker := filepath.Join(r.dir, "a.iso.laria")
	if !lexists(marker) {
		t.Fatal("no marker")
	}
	got := r.complete(t, j, "a.iso")
	if got.State != store.Done {
		t.Fatalf("state %s", got.State)
	}
	if names := listDir(t, r.dir); !slices.Equal(names, []string{"a.iso"}) {
		t.Fatalf("after publish the download folder holds %v", names)
	}

	k := startStaged(t, r, "b.iso")
	marker = filepath.Join(r.dir, "b.iso.laria")
	if !lexists(marker) {
		t.Fatal("no marker for the second job")
	}
	if _, _, err := r.m.Remove(bg, []string{k.ID}, false); err != nil {
		t.Fatal(err)
	}
	if lexists(marker) {
		t.Fatal("marker left after remove")
	}
}

func TestMarkerSettingOff(t *testing.T) {
	r := newRig(t)
	r.m.s.Load().Downloads.Marker = false
	activeStaged(t, r)
	marker := filepath.Join(r.dir, "a.iso.laria")
	if lexists(marker) {
		t.Fatal("marker made with the setting off")
	}
	// The setting is live both ways.
	r.m.s.Load().Downloads.Marker = true
	r.m.poll(bg)
	if !lexists(marker) {
		t.Fatal("no marker once the setting is on")
	}
	r.m.s.Load().Downloads.Marker = false
	r.m.poll(bg)
	if lexists(marker) {
		t.Fatal("marker kept once the setting is off")
	}
}

func TestReconcileRemovesDanglingMarker(t *testing.T) {
	r := newRig(t)
	r.m.s.Load().Downloads.Dir = r.dir
	j, _ := activeStaged(t, r)
	live := filepath.Join(r.dir, "a.iso.laria")
	dangling := filepath.Join(r.dir, "x.bin.laria")
	if err := os.Symlink(filepath.Join(".laria", store.NewID(), "x.bin"), dangling); err != nil {
		t.Fatal(err)
	}
	notOurs := filepath.Join(r.dir, "y.laria")
	if err := os.Symlink(filepath.Join("elsewhere", "y"), notOurs); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcile(); err != nil {
		t.Fatal(err)
	}
	if lexists(dangling) {
		t.Fatal("dangling marker left")
	}
	if linkTo(t, live) != markerOf(j, "a.iso") || !lexists(notOurs) {
		t.Fatal("reconcile removed a live marker or a link not ours")
	}
}
