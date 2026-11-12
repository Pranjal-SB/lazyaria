package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// trashedEvent returns the trashed path recorded for job id under kind.
func trashedEvent(t *testing.T, r *rig, id, kind string) string {
	t.Helper()
	evs, _ := r.st.Events(bg, id)
	for _, e := range evs {
		if e.Kind == kind {
			return e.Detail
		}
	}
	return ""
}

// A re-add resumes (continue=true) only with the job's own control file
// there. Only a true partial of its own (same drive, regular, shorter than
// the known total, no .aria2) goes to the trash; nothing is deleted.
func TestReaddOwnership(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	wrote, none, total := int64(10), int64(0), int64(100)
	started := r.clk.now()
	for _, c := range []struct {
		name               string
		uri, out           string
		done, total        *int64
		ctl, dir           bool // a .aria2 beside it; Out is a directory
		noContinue, trashd bool
	}{
		{"no out", "http://example.com/", "", &wrote, &total, false, false, true, false},
		{"out, no bytes recorded", "http://example.com/a", "a.bin", nil, &total, false, false, true, false},
		{"out, zero bytes", "http://example.com/b", "b.bin", &none, &total, false, false, true, false},
		{"own partial", "http://example.com/c", "c.bin", &wrote, &total, false, false, true, true},
		{"own partial with .aria2", "http://example.com/d", "d.bin", &wrote, &total, true, false, false, false},
		{"unknown total", "http://example.com/e", "e.bin", &wrote, nil, false, false, true, false},
		{"not a regular file", "http://example.com/f", "f.bin", &wrote, &total, false, true, true, false},
		{"owned torrent", "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", "", &wrote, &total, false, false, false, false},
	} {
		j := r.add(t, AddParams{URIs: []string{c.uri}, Out: c.out, Force: true})
		j.DoneBytes, j.TotalBytes, j.StartedAt = c.done, c.total, &started
		if c.name == "owned torrent" {
			out := "t" // aria2 reported a path for the torrent
			j.Out = &out
		}
		var p string
		if j.Out != nil {
			p = filepath.Join(r.dir, *j.Out)
			if c.dir {
				os.Mkdir(p, 0o700)
			} else {
				os.WriteFile(p, []byte("part"), 0o600)
			}
			if c.ctl {
				os.WriteFile(p+".aria2", []byte("ctl"), 0o600)
			}
		}
		if _, err := r.m.readd(bg, j, false, "readded"); err != nil {
			t.Fatal(err)
		}
		req := r.e.adds[len(r.e.adds)-1]
		if _, ok := req.Options["allow-overwrite"]; ok || req.NoContinue != c.noContinue {
			t.Errorf("%s: continue=false %v options %v; want continue=false %v", c.name, req.NoContinue, req.Options, c.noContinue)
		}
		if p != "" && exists(p) == c.trashd {
			t.Errorf("%s: file gone %v, want %v", c.name, !exists(p), c.trashd)
		}
		if got := trashedEvent(t, r, j.ID, "trashed-partial") != ""; got != c.trashd {
			t.Errorf("%s: trashed-partial event %v, want %v", c.name, got, c.trashd)
		}
		if got := r.get(t, j.ID); c.trashd && ownsFile(got) {
			t.Errorf("%s: still owns its path after the trash", c.name)
		}
	}
}

// noRangePartial adds a known-size job that wrote 100 bytes of a.bin with
// no control file, then failed.
func noRangePartial(t *testing.T, r *rig) (*store.Job, string) {
	t.Helper()
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.bin"}, Out: "a.bin"})
	path := filepath.Join(r.dir, "a.bin")
	j = r.m.apply(bg, j, engine.EngineStatus{State: "active", Done: 100, Total: 1000, Path: path}, "")
	os.WriteFile(path, []byte("partial"), 0o600)
	r.m.apply(bg, j, engine.EngineStatus{State: "error", ErrorCode: 1, Path: path}, "")
	return r.get(t, j.ID), path
}

// Probe p7: the own partial is trashed on retry, the next attempt fails
// before any reply, the user saves a.bin, and the retry after that must
// leave the user's file alone and download beside it.
func TestP7UserFileAfterTrashedPartialSurvives(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j, path := noRangePartial(t, r)
	if _, err := r.m.readd(bg, j, false, "resumed"); err != nil {
		t.Fatal(err)
	}
	if exists(path) {
		t.Fatal("own partial not trashed")
	}
	j = r.m.apply(bg, r.get(t, j.ID), engine.EngineStatus{State: "error", ErrorCode: 1, Path: path}, "")
	os.WriteFile(path, []byte("USER FILE"), 0o600)
	if _, err := r.m.readd(bg, r.get(t, j.ID), false, "resumed"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "USER FILE" {
		t.Fatalf("user's a.bin now %q", b)
	}
	if !r.e.adds[len(r.e.adds)-1].NoContinue {
		t.Fatal("re-add would resume into the user's file")
	}
}

// Probe p7b: another drive mounted at the dir holds a file at Out; it is
// not the job's and is never touched.
func TestP7bOtherDriveFileUntouched(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j, path := noRangePartial(t, r)
	r.m.dev = func(string) (uint64, error) { return 99, nil }
	os.WriteFile(path, []byte("OTHER DRIVE FILE"), 0o600)
	if _, err := r.m.readd(bg, j, false, "resumed"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "OTHER DRIVE FILE" || !r.e.adds[len(r.e.adds)-1].NoContinue {
		t.Fatalf("other drive's a.bin now %q", b)
	}
	if ownsFile(r.get(t, j.ID)) {
		t.Fatal("job still owns the other drive's file")
	}
}

// A trash failure never fails the re-add and never deletes: the new
// download goes beside the partial.
func TestReaddTrashFailureGoesBeside(t *testing.T) {
	r := newRig(t)
	j, path := noRangePartial(t, r)
	r.m.trash = func(string) (trash.Entry, error) { return trash.Entry{}, trash.ErrNoTrash }
	if _, err := r.m.readd(bg, j, false, "resumed"); err != nil {
		t.Fatal(err)
	}
	if !exists(path) || !r.e.adds[len(r.e.adds)-1].NoContinue {
		t.Fatal("partial gone, or the re-add would resume into it")
	}
}

func TestTrashedPartialIsRestorable(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	t.Setenv("XDG_DATA_HOME", data)
	r := newRig(t)
	j, path := noRangePartial(t, r)
	if _, err := r.m.readd(bg, j, false, "resumed"); err != nil {
		t.Fatal(err)
	}
	trashed := trashedEvent(t, r, j.ID, "trashed-partial")
	e := trash.Entry{TrashedPath: trashed, Original: path,
		InfoPath: filepath.Join(data, "Trash", "info", filepath.Base(trashed)+".trashinfo")}
	if err := trash.Restore(e); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "partial" {
		t.Fatalf("restored %q", b)
	}
}

// on_exists = overwrite trashes only a regular file; a directory there is
// renamed beside. A trash failure leaves no job row behind.
func TestOverwriteOnlyRegularAndUndoneOnTrashFailure(t *testing.T) {
	r := newRig(t)
	r.m.s.Load().Reliability.Duplicates = "allow"
	r.m.s.Load().Downloads.OnExists = "overwrite"
	os.Mkdir(filepath.Join(r.dir, "d.bin"), 0o700)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/d.bin"}})
	if j.Name != "d.1.bin" {
		t.Fatalf("directory target: name %q, want d.1.bin", j.Name)
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
