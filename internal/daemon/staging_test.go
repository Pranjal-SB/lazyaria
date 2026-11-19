package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestAddDownloadsIntoStaging(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	if !j.Staged || !r.get(t, j.ID).Staged {
		t.Fatal("new job is not staged")
	}
	want := filepath.Join(r.dir, ".laria", j.ID)
	if got := stagingDir(j); got != want {
		t.Fatalf("stagingDir = %s, want %s", got, want)
	}
	req := r.e.adds[0]
	if req.Dir != want || req.Out != "" || req.NoContinue {
		t.Fatalf("request dir %q out %q continue=false %v; want dir %q, no out, continue allowed", req.Dir, req.Out, req.NoContinue, want)
	}
	fi, err := os.Stat(want)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o755 {
		t.Fatalf("staging folder: %v %v", fi, err)
	}
	if j.Dir != r.dir {
		t.Fatalf("job dir %q, want the download folder %q", j.Dir, r.dir)
	}
}

func TestAddKeepsUserOut(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "x.bin"})
	req := r.e.adds[0]
	if req.Out != "x.bin" || req.Dir != stagingDir(j) || j.Out == nil || *j.Out != "x.bin" {
		t.Fatalf("request out %q dir %q, job out %v", req.Out, req.Dir, j.Out)
	}
}

func TestAddStillRefusesExistingTarget(t *testing.T) {
	setup := func(t *testing.T, mode string) *rig {
		r := newRig(t)
		r.m.s.Load().Reliability.Duplicates = "allow"
		r.m.s.Load().Downloads.OnExists = mode
		os.WriteFile(filepath.Join(r.dir, "a.iso"), []byte("mine"), 0o600)
		return r
	}
	t.Run("ask", func(t *testing.T) {
		r := setup(t, "ask")
		_, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/a.iso"}, Dir: r.dir})
		if _, ok := errors.AsType[*ExistsError](err); !ok {
			t.Fatalf("want ExistsError, got %v", err)
		}
		if len(r.e.adds) != 0 {
			t.Fatal("added anyway")
		}
	})
	t.Run("rename", func(t *testing.T) {
		r := setup(t, "rename")
		j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
		// The final name is picked at publish, not now.
		if req := r.e.adds[0]; j.Out != nil || req.Out != "" || j.Name != "a.iso" {
			t.Fatalf("job out %v name %q, request out %q", j.Out, j.Name, req.Out)
		}
		if b, _ := os.ReadFile(filepath.Join(r.dir, "a.iso")); string(b) != "mine" {
			t.Fatal("the user's file changed")
		}
	})
}

func TestTrackNameInStaging(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/"}})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) {
		s.State, s.Path = "active", filepath.Join(stagingDir(j), "srv-name.bin")
	})
	r.event(ref, engine.KindStart)
	got := r.get(t, j.ID)
	if got.Out == nil || *got.Out != "srv-name.bin" || got.Name != "srv-name.bin" {
		t.Fatalf("out %v name %q, want srv-name.bin", got.Out, got.Name)
	}
}

func TestLegacyUnfinishedJobsStop(t *testing.T) {
	r := newRig(t)
	part := filepath.Join(r.dir, "f.bin")
	os.WriteFile(part, []byte("half"), 0o600)
	os.WriteFile(part+".aria2", []byte("ctl"), 0o600)
	out := "f.bin"
	var legacy []*store.Job
	for i, st := range []store.State{store.Active, store.Queued, store.Paused, store.RetryWait} {
		ref := []string{"0000000000000001", "0000000000000002", "0000000000000003", "0000000000000004"}[i]
		legacy = append(legacy, r.insert(t, st, ref, func(j *store.Job) { j.Out, j.Staged = &out, false }))
		r.e.setState(engine.EngineRef(ref), map[store.State]string{store.Active: "active", store.Queued: "waiting"}[st])
	}
	done := r.insert(t, store.Done, "", func(j *store.Job) { j.Out, j.Staged = &out, false })
	staged := r.insert(t, store.Paused, "00000000000000aa", nil)
	r.e.setState("00000000000000aa", "paused")

	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	want := "started by an older lazyaria; its file is untouched in " + r.dir + "; add the link again to download it with staging"
	for _, l := range legacy {
		got := r.get(t, l.ID)
		if got.State != store.Error || got.ErrorKind == nil || *got.ErrorKind != "legacy" || got.ErrorMsg == nil || *got.ErrorMsg != want {
			t.Fatalf("legacy %s row: state %s kind %v msg %v", l.State, got.State, got.ErrorKind, got.ErrorMsg)
		}
		if !slices.Contains(r.e.removed, refOf(l)) {
			t.Errorf("legacy %s download not removed from the engine", l.State)
		}
	}
	if len(r.e.adds) != 0 {
		t.Fatalf("legacy jobs re-added: %+v", r.e.adds)
	}
	if b, _ := os.ReadFile(part); string(b) != "half" {
		t.Fatal("the legacy file changed")
	}
	if !exists(part + ".aria2") {
		t.Fatal("the legacy control file is gone")
	}
	if got := r.get(t, done.ID); got.State != store.Done || got.ErrorKind != nil {
		t.Fatalf("legacy done row changed: %s %v", got.State, got.ErrorKind)
	}
	if got := r.get(t, staged.ID); got.State != store.Paused {
		t.Fatalf("staged row: %s", got.State)
	}
	// Resume and refresh never start it again in the download folder.
	if err := r.m.Resume(bg, []string{legacy[0].ID}); err == nil {
		t.Fatal("resume of a legacy job: want an error")
	}
	if _, err := r.m.RefreshURI(bg, legacy[1].ID, "http://example.com/new"); err == nil {
		t.Fatal("refresh of a legacy job: want an error")
	}
	if len(r.e.adds) != 0 {
		t.Fatalf("legacy job re-added: %+v", r.e.adds)
	}
}
