package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// A slow trash move (minutes across disks) must not hold up other calls,
// and the job being removed must not be touched by them meanwhile.
func TestRemoveMovesFilesWithoutTheLock(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	entered, release := make(chan struct{}), make(chan struct{})
	r.m.Files = &TrashFiles{trash: func(p string) (trash.Entry, error) {
		close(entered)
		<-release
		return trash.Trash(p)
	}}
	j, part := activeStaged(t, r)

	type result struct {
		tok string
		err error
	}
	done := make(chan result, 1)
	go func() {
		tok, _, err := r.m.Remove(bg, []string{j.ID}, true)
		done <- result{tok, err}
	}()
	<-entered

	added := make(chan error, 1)
	go func() {
		_, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/b.iso"}, Dir: r.dir})
		added <- err
	}()
	select {
	case err := <-added:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("add waited for the trash move")
	}

	// The job is already out of reach: no action brings it back or
	// trashes it twice while its files move.
	if got := r.get(t, j.ID); got.State != store.Removed {
		t.Fatalf("state %s while its files move", got.State)
	}
	if err := r.m.Resume(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	if tok, _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil || tok != "" {
		t.Fatalf("second remove: %q %v", tok, err)
	}
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if got := r.get(t, j.ID); got.State != store.Removed {
		t.Fatalf("state %s after resume and reconcile", got.State)
	}

	close(release)
	res := <-done
	if res.err != nil || res.tok == "" {
		t.Fatalf("remove: %q %v", res.tok, res.err)
	}
	if exists1(stagingDir(j)) {
		t.Fatal("staging folder not trashed")
	}
	if err := r.m.Undo(bg, res.tok); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(part); string(b) != "half" || r.get(t, j.ID).State != store.Paused {
		t.Fatal("undo did not bring the job back")
	}
}
