package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// liveTorrent starts a multi-file torrent job named Music that aria2 reports
// writing a.flac and b.flac.
func liveTorrent(t *testing.T, r *rig) (id, music string) {
	t.Helper()
	j := r.add(t, AddParams{Torrent: []byte("d4:infod4:name5:Musicee")})
	ref := r.ref(t, j.ID)
	music = filepath.Join(r.dir, "Music")
	r.e.set(ref, func(s *engine.EngineStatus) {
		s.State, s.IsBT, s.Name, s.Path, s.Done, s.Total = "active", true, "Music", music, 4, 100
	})
	r.e.details[ref] = engine.EngineDetails{Files: []engine.EngineFile{
		{Path: filepath.Join(music, "a.flac"), Selected: true}, {Path: filepath.Join(music, "sub", "b.flac"), Selected: true},
	}}
	r.event(ref, engine.KindStart)
	return j.ID, music
}

// The torrent writes into a Music folder the user already had. Removing it
// with its files takes only what the torrent wrote.
func TestRemoveTorrentKeepsFolderItDidNotCreate(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	id, music := liveTorrent(t, r)
	os.MkdirAll(filepath.Join(music, "sub"), 0o755)
	wfile(t, filepath.Join(music, "mine.flac"), "the user's")
	wfile(t, filepath.Join(music, "a.flac"), "a")
	wfile(t, filepath.Join(music, "sub", "b.flac"), "b")
	wfile(t, music+".aria2", "ctl")

	tok, _, err := r.m.Remove(bg, []string{id}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !exists1(filepath.Join(music, "mine.flac")) {
		t.Fatal("the user's mine.flac was trashed")
	}
	for _, p := range []string{filepath.Join(music, "a.flac"), filepath.Join(music, "sub"), music + ".aria2"} {
		if exists1(p) {
			t.Errorf("%s left behind", p)
		}
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if !exists1(filepath.Join(music, "sub", "b.flac")) || !exists1(filepath.Join(music, "a.flac")) {
		t.Fatal("undo did not bring the torrent's files back")
	}
}

// The payload folder itself is never removed: it may be the user's, with
// their own mode. Folders below it that the remove emptied go, and Undo
// builds them again.
func TestRemoveTorrentKeepsEmptiedPayloadFolder(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	id, music := liveTorrent(t, r)
	os.MkdirAll(filepath.Join(music, "sub"), 0o755)
	os.Chmod(music, 0o700)
	wfile(t, filepath.Join(music, "a.flac"), "a")
	wfile(t, filepath.Join(music, "sub", "b.flac"), "b")

	tok, _, err := r.m.Remove(bg, []string{id}, true)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(music); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("Music folder not kept as it was: %v %v", fi, err)
	}
	if exists1(filepath.Join(music, "sub")) {
		t.Fatal("emptied sub folder left behind")
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if !exists1(filepath.Join(music, "sub", "b.flac")) {
		t.Fatal("undo did not bring the files back")
	}
}

// Once aria2 has forgotten a finished torrent, the stored .torrent says
// which files are its own.
func TestRemoveFinishedTorrentUsesStoredFileList(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	tor := "d4:infod5:filesld6:lengthi1e4:pathl6:a.flaceed6:lengthi1e4:pathl3:sub6:b.flaceee4:name5:Musicee"
	j := r.add(t, AddParams{Torrent: []byte(tor)})
	if _, err := r.m.change(bg, j.ID, "", "", func(x *store.Job) { x.State, x.Name, x.EngineRef = store.Done, "Music", nil }); err != nil {
		t.Fatal(err)
	}
	music := filepath.Join(r.dir, "Music")
	os.MkdirAll(filepath.Join(music, "sub"), 0o755)
	wfile(t, filepath.Join(music, "mine.flac"), "the user's")
	wfile(t, filepath.Join(music, "a.flac"), "a")
	wfile(t, filepath.Join(music, "sub", "b.flac"), "b")

	if _, _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil {
		t.Fatal(err)
	}
	if !exists1(filepath.Join(music, "mine.flac")) {
		t.Fatal("the user's mine.flac was trashed")
	}
	if exists1(filepath.Join(music, "a.flac")) || exists1(filepath.Join(music, "sub")) {
		t.Fatal("the torrent's files were left behind")
	}
}

func TestTorrentPaths(t *testing.T) {
	for _, tc := range []struct {
		tor  string
		want []string
	}{
		{"d4:infod6:lengthi1e4:name5:x.isoee", []string{"/d/x.iso"}},
		{"d4:infod5:filesld6:lengthi1e4:pathl3:sub1:aeee4:name1:Mee", []string{"/d/M/sub/a"}},
		{"d4:infod5:filesld6:lengthi1e4:pathl2:..1:aeee4:name1:Mee", nil}, // escapes the folder
		{"d4:infod4:name2:..ee", nil},
		{"d4:infod10:name.utf-81:.ee", nil}, // the download folder itself
		{"d4:infod5:filesld6:lengthi1e4:pathl1:.eee4:name1:Mee", nil},
		{"garbage", nil},
		{"d4:infod5:filesl", nil},
		{"lllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllll", nil},
	} {
		if got := torrentPaths([]byte(tc.tor), "/d"); !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.tor, got, tc.want)
		}
	}
}

// With select-file the user may hold a file the torrent lists but never
// wrote. aria2 lists unselected files too; they are not the torrent's.
func TestRemoveTorrentSkipsUnselectedFiles(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	id, music := liveTorrent(t, r)
	ref := r.ref(t, id)
	r.e.details[ref] = engine.EngineDetails{Files: []engine.EngineFile{
		{Path: filepath.Join(music, "a.flac"), Selected: true}, {Path: filepath.Join(music, "sub", "b.flac")},
	}}
	os.MkdirAll(filepath.Join(music, "sub"), 0o755)
	wfile(t, filepath.Join(music, "a.flac"), "a")
	wfile(t, filepath.Join(music, "sub", "b.flac"), "the user's")

	if _, _, err := r.m.Remove(bg, []string{id}, true); err != nil {
		t.Fatal(err)
	}
	if !exists1(filepath.Join(music, "sub", "b.flac")) {
		t.Fatal("the user's sub/b.flac was trashed")
	}
	if exists1(filepath.Join(music, "a.flac")) {
		t.Fatal("a.flac left behind")
	}
}

// The stored .torrent cannot say which files select-file picked, so a
// finished job with it keeps its folder rather than guess.
func TestRemoveFinishedTorrentWithSelectFileKeepsFiles(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	tor := "d4:infod5:filesld6:lengthi1e4:pathl6:a.flaceed6:lengthi1e4:pathl3:sub6:b.flaceee4:name5:Musicee"
	j := r.add(t, AddParams{Torrent: []byte(tor), Options: map[string]string{"select-file": "1"}})
	if _, err := r.m.change(bg, j.ID, "", "", func(x *store.Job) { x.State, x.Name, x.EngineRef = store.Done, "Music", nil }); err != nil {
		t.Fatal(err)
	}
	music := filepath.Join(r.dir, "Music")
	os.MkdirAll(filepath.Join(music, "sub"), 0o755)
	wfile(t, filepath.Join(music, "a.flac"), "a")
	wfile(t, filepath.Join(music, "sub", "b.flac"), "the user's")

	if _, _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil {
		t.Fatal(err)
	}
	if !exists1(filepath.Join(music, "sub", "b.flac")) || !exists1(filepath.Join(music, "a.flac")) {
		t.Fatal("files were trashed on a guess")
	}
}

// The trash records resolved paths, so a download folder reached through a
// symlink must still have its emptied folders pruned.
func TestRemoveTorrentPrunesThroughSymlinkedDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	link := filepath.Join(t.TempDir(), "dl")
	if err := os.Symlink(r.dir, link); err != nil {
		t.Fatal(err)
	}
	r.dir = link
	id, music := liveTorrent(t, r)
	os.MkdirAll(filepath.Join(music, "sub"), 0o755)
	wfile(t, filepath.Join(music, "a.flac"), "a")
	wfile(t, filepath.Join(music, "sub", "b.flac"), "b")

	if _, _, err := r.m.Remove(bg, []string{id}, true); err != nil {
		t.Fatal(err)
	}
	if exists1(filepath.Join(music, "sub", "b.flac")) {
		t.Fatal("b.flac left behind")
	}
	if exists1(filepath.Join(music, "sub")) {
		t.Fatal("emptied sub folder left behind")
	}
}

// A finished torrent stamps only the files it wrote, not the user's files
// sharing its folder.
func TestDoneStampsOnlyTheTorrentsFiles(t *testing.T) {
	r := newRig(t)
	r.m.s.Load().Privacy.StampOriginXattr = true
	j := r.add(t, AddParams{URIs: []string{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}})
	ref := r.ref(t, j.ID)
	music := filepath.Join(r.dir, "Music")
	os.MkdirAll(music, 0o755)
	mine, ours := filepath.Join(music, "mine.flac"), filepath.Join(music, "a.flac")
	wfile(t, mine, "the user's")
	wfile(t, ours, "a")
	if err := unix.Setxattr(ours, "user.probe", []byte("1"), 0); err != nil {
		t.Skipf("no user xattrs here: %v", err)
	}
	r.e.set(ref, func(s *engine.EngineStatus) {
		s.State, s.IsBT, s.Name, s.Path, s.Done, s.Total = "complete", true, "Music", music, 1, 1
	})
	r.e.details[ref] = engine.EngineDetails{Files: []engine.EngineFile{{Path: ours, Selected: true}}}
	r.event(ref, engine.KindComplete)
	if got := r.get(t, j.ID); got.State != store.Done {
		t.Fatalf("state %s", got.State)
	}
	if origin(ours) == "" {
		t.Fatal("the torrent's file was not stamped")
	}
	if got := origin(mine); got != "" {
		t.Fatalf("the user's file was stamped with %q", got)
	}
}

// A finished magnet aria2 has forgotten has no file list, so its folder is
// kept, and the remove says so instead of looking like it deleted it.
func TestRemoveFinishedMagnetReportsKeptFolder(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}})
	if _, err := r.m.change(bg, j.ID, "", "", func(x *store.Job) { x.State, x.Name, x.EngineRef = store.Done, "Music", nil }); err != nil {
		t.Fatal(err)
	}
	music := filepath.Join(r.dir, "Music")
	os.MkdirAll(music, 0o755)
	wfile(t, filepath.Join(music, "a.flac"), "a")

	_, kept, err := r.m.Remove(bg, []string{j.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kept, []string{music}) || !exists1(filepath.Join(music, "a.flac")) {
		t.Fatalf("kept %v; want the Music folder kept and reported", kept)
	}
}
