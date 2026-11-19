package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

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
