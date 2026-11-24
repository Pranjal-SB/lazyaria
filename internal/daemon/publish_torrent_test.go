package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/fsutil"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

var (
	musicTorrent = []byte("d4:infod5:filesld6:lengthi1e4:pathl6:a.flaceed6:lengthi1e4:pathl3:sub6:b.flaceee4:name5:Musicee")
	isoTorrent   = []byte("d4:infod6:lengthi4e4:name5:x.isoee")
	musicFiles   = map[string]string{"Music/a.flac": "a", "Music/sub/b.flac": "b"}
)

// seedTorrent adds a torrent, puts its payload in staging and reports it
// finished and seeding, as aria2 does at bt-complete.
func (r *rig) seedTorrent(t *testing.T, torrent []byte, name string, files map[string]string) *store.Job {
	t.Helper()
	return r.seedTorrent2(t, r.add(t, AddParams{Torrent: torrent}), name, files)
}

// seedTorrent2 is seedTorrent for a job already added.
func (r *rig) seedTorrent2(t *testing.T, j *store.Job, name string, files map[string]string) *store.Job {
	t.Helper()
	stageFiles(t, j, files)
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) {
		s.State, s.IsBT, s.Seeder, s.Name, s.Done, s.Total = "active", true, true, name, 4, 4
	})
	r.event(ref, engine.KindBTComplete)
	r.settle()
	return r.get(t, j.ID)
}

func ino(t *testing.T, p string) uint64 {
	t.Helper()
	i, _, _, err := fsutil.FileID(p)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestTorrentPublishesByLinksWhileSeeding(t *testing.T) {
	r := newRig(t)
	j := r.seedTorrent(t, musicTorrent, "Music", musicFiles)
	if j.State != store.Seeding || !j.Staged || publishing(j) || j.Name != "Music" {
		t.Fatalf("job: state %s staged %v publishing %v name %q", j.State, j.Staged, publishing(j), j.Name)
	}
	for _, f := range []string{"a.flac", "sub/b.flac"} {
		pub, staged := filepath.Join(r.dir, "Music", f), filepath.Join(stagingDir(j), "Music", f)
		if ino(t, pub) != ino(t, staged) {
			t.Fatalf("%s is not a hard link of the staging copy", f)
		}
	}
	if names := listDir(t, r.dir); !slices.Equal(names, []string{".laria", "Music"}) {
		t.Fatalf("download folder holds %v", names)
	}
	if exists1(filepath.Join(stagingDir(j), upDir)) {
		t.Fatal("the link tree was left in staging")
	}
	if len(r.e.removed) != 0 {
		t.Fatalf("engine removed %v; seeding must go on", r.e.removed)
	}
	if !r.hasEvent(t, j.ID, "published") {
		t.Fatal("no published event")
	}
}

// The name is taken in the download folder: the user's folder stays as it
// is and ours is published beside it.
func TestTorrentPublishPicksFreeName(t *testing.T) {
	r := newRig(t)
	wfile(t, filepath.Join(r.dir, "Music", "mine.flac"), "the user's")
	j := r.seedTorrent(t, musicTorrent, "Music", musicFiles)
	if j.State != store.Seeding || j.Name != "Music.1" {
		t.Fatalf("state %s name %q", j.State, j.Name)
	}
	if names := listDir(t, filepath.Join(r.dir, "Music")); !slices.Equal(names, []string{"mine.flac"}) {
		t.Fatalf("the user's folder now holds %v", names)
	}
	if read(t, filepath.Join(r.dir, "Music.1", "sub", "b.flac")) != "b" {
		t.Fatal("ours not published as Music.1")
	}
}

// A crash after the intent is recorded: reconcile finishes the rename and
// the torrent goes on seeding.
func TestTorrentLinkPublishResumesAfterCrash(t *testing.T) {
	r := newRig(t)
	stopAt(t, "intent")
	j := r.seedTorrent(t, musicTorrent, "Music", musicFiles)
	if !publishing(j) || exists1(filepath.Join(r.dir, "Music")) {
		t.Fatalf("stopped publish: publishing %v", publishing(j))
	}
	publishStop = func(string) bool { return false }
	if err := r.reconcile(); err != nil {
		t.Fatal(err)
	}
	got := r.get(t, j.ID)
	if got.State != store.Seeding || !got.Staged || publishing(got) {
		t.Fatalf("state %s staged %v publishing %v", got.State, got.Staged, publishing(got))
	}
	if ino(t, filepath.Join(r.dir, "Music", "a.flac")) != ino(t, filepath.Join(stagingDir(j), "Music", "a.flac")) {
		t.Fatal("not published by links")
	}
}

func TestTorrentSeedingEndDropsStaging(t *testing.T) {
	for _, how := range []string{"complete", "stop-seeding", "engine-lost"} {
		t.Run(how, func(t *testing.T) {
			r := newRig(t)
			j := r.seedTorrent(t, musicTorrent, "Music", musicFiles)
			ref := r.ref(t, j.ID)
			switch how {
			case "complete":
				r.e.setState(ref, "complete")
				r.event(ref, engine.KindComplete)
			case "stop-seeding":
				if err := r.m.StopSeeding(bg, []string{j.ID}); err != nil {
					t.Fatal(err)
				}
			case "engine-lost":
				r.e.Remove(bg, ref)
				r.m.poll(bg)
			}
			got := r.get(t, j.ID)
			if got.State != store.Done || got.Staged || got.Name != "Music" {
				t.Fatalf("state %s staged %v name %q", got.State, got.Staged, got.Name)
			}
			if exists1(filepath.Join(r.dir, ".laria")) {
				t.Fatal("staging left behind")
			}
			if read(t, filepath.Join(r.dir, "Music", "a.flac")) != "a" || read(t, filepath.Join(r.dir, "Music", "sub", "b.flac")) != "b" {
				t.Fatal("published files changed")
			}
		})
	}
}

// failLink makes hard links fail as on FAT and exFAT.
func failLink(t *testing.T) {
	t.Helper()
	orig := publishLink
	publishLink = func(o, n string) error { return &os.LinkError{Op: "link", Old: o, New: n, Err: syscall.EPERM} }
	t.Cleanup(func() { publishLink = orig })
}

func TestTorrentLinkFallback(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		damage     bool
	}{{"verified", "unverified", false}, {"size mismatch", "check", true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			failLink(t)
			if tc.damage {
				orig := publishRename
				publishRename = func(o, n string) error {
					err := orig(o, n)
					f, _ := os.OpenFile(n, os.O_APPEND|os.O_WRONLY, 0)
					f.WriteString("!")
					f.Close()
					return err
				}
				t.Cleanup(func() { publishRename = orig })
			}
			j := r.add(t, AddParams{Torrent: isoTorrent})
			old := r.ref(t, j.ID)
			j = r.seedTorrent2(t, j, "x.iso", map[string]string{"x.iso": "data"})
			if !slices.Contains(r.e.removed, old) {
				t.Fatalf("engine removed %v; want the old download %s dropped", r.e.removed, old)
			}
			if len(r.e.adds) != 2 {
				t.Fatalf("adds %d, want the re-add", len(r.e.adds))
			}
			add := r.e.adds[1]
			if add.SeedFrom != tc.want || add.Dir != r.dir || string(add.Torrent) != string(isoTorrent) {
				t.Fatalf("re-add: seed from %q dir %q", add.SeedFrom, add.Dir)
			}
			if j.State != store.Seeding || j.Staged || j.EngineRef == nil {
				t.Fatalf("job: state %s staged %v ref %v", j.State, j.Staged, j.EngineRef)
			}
			if !exists1(filepath.Join(r.dir, "x.iso")) || exists1(filepath.Join(r.dir, ".laria")) {
				t.Fatalf("download folder holds %v", listDir(t, r.dir))
			}
		})
	}
}

func TestRemoveSeedingTorrentTrashesBothNames(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j := r.seedTorrent(t, musicTorrent, "Music", musicFiles)
	tok, _, err := r.m.Remove(bg, []string{j.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if exists1(filepath.Join(r.dir, "Music")) || exists1(filepath.Join(r.dir, ".laria")) {
		t.Fatalf("download folder still holds %v", listDir(t, r.dir))
	}
	trashed, _ := filepath.Glob(filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "files", "*"))
	if len(trashed) != 1 || filepath.Base(trashed[0]) != "Music" {
		t.Fatalf("trash holds %v; want only the published copy", trashed)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(r.dir, "Music", "sub", "b.flac")) != "b" {
		t.Fatal("undo did not restore the published copy")
	}
	got := r.get(t, j.ID)
	if got.State != store.Done || got.Staged {
		t.Fatalf("after undo: state %s staged %v", got.State, got.Staged)
	}
}

// Removing a seeding torrent without its files keeps the published copy and
// drops the staging one.
func TestRemoveSeedingTorrentKeepsPublishedCopy(t *testing.T) {
	r := newRig(t)
	j := r.seedTorrent(t, musicTorrent, "Music", musicFiles)
	if _, _, err := r.m.Remove(bg, []string{j.ID}, false); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(r.dir, "Music", "a.flac")) != "a" || exists1(filepath.Join(r.dir, ".laria")) {
		t.Fatalf("download folder holds %v", listDir(t, r.dir))
	}
	if got := r.get(t, j.ID); got.Staged {
		t.Fatal("still staged")
	}
}
