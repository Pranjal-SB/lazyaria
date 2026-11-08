package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

func wfile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists1(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestRemoveUndo(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	payload := filepath.Join(r.dir, "a.iso")
	wfile(t, payload, "data")
	wfile(t, payload+".aria2", "ctl")

	tok, err := r.m.Remove(bg, []string{j.ID}, true)
	if err != nil || tok == "" {
		t.Fatalf("remove: %q %v", tok, err)
	}
	if exists1(payload) || exists1(payload+".aria2") {
		t.Fatal("files still in dir")
	}
	if got := r.get(t, j.ID); got.State != store.Removed {
		t.Fatalf("state %s", got.State)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(payload); string(b) != "data" || !exists1(payload+".aria2") {
		t.Fatal("files not restored")
	}
	if got := r.get(t, j.ID); got.State != store.Paused {
		t.Fatalf("state %s", got.State)
	}

	tok, err = r.m.Remove(bg, []string{j.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	r.clk.add(11 * time.Second)
	if err := r.m.Undo(bg, tok); !errors.Is(err, ErrUndoExpired) {
		t.Fatalf("want ErrUndoExpired, got %v", err)
	}
	if exists1(payload) {
		t.Fatal("expired undo must leave files in the trash")
	}
}

func TestRemoveNoTrashNeedsConfirm(t *testing.T) {
	r := newRig(t)
	r.m.Files = &TrashFiles{trash: func(string) (trash.Entry, error) { return trash.Entry{}, trash.ErrNoTrash }}
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	payload := filepath.Join(r.dir, "a.iso")
	wfile(t, payload, "data")
	tok, err := r.m.Remove(bg, []string{j.ID}, true)
	var nc *NeedsConfirmError
	if !errors.As(err, &nc) {
		t.Fatalf("want NeedsConfirmError, got %v", err)
	}
	if tok != "" {
		t.Fatalf("token %q for a remove that did not happen", tok)
	}
	if !exists1(payload) {
		t.Fatal("file was deleted")
	}
	if got := r.get(t, j.ID); got.State == store.Removed {
		t.Fatal("row removed")
	}
}

func TestRemoveRollsBackOnPartialTrash(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	n := 0
	r.m.Files = &TrashFiles{trash: func(p string) (trash.Entry, error) {
		if n++; n == 2 {
			return trash.Entry{}, trash.ErrNoTrash
		}
		return trash.Trash(p)
	}}
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	payload := filepath.Join(r.dir, "a.iso")
	wfile(t, payload, "data")
	wfile(t, payload+".aria2", "ctl")
	_, err := r.m.Remove(bg, []string{j.ID}, true)
	var nc *NeedsConfirmError
	if !errors.As(err, &nc) || !exists1(payload) || !exists1(payload+".aria2") {
		t.Fatalf("err %v, payload back %v", err, exists1(payload))
	}
}

// finish adds a job, drops file (if any) into its dir, then completes it.
func finish(t *testing.T, r *rig, p AddParams, file string) *store.Job {
	t.Helper()
	j := r.add(t, p)
	if file != "" {
		wfile(t, filepath.Join(r.dir, file), "data")
	}
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State = "complete" })
	r.event(ref, engine.KindComplete)
	return r.get(t, j.ID)
}

func origin(p string) string {
	buf := make([]byte, 512)
	n, err := unix.Getxattr(p, "user.xdg.origin.url", buf)
	if err != nil {
		return ""
	}
	return string(buf[:n])
}

func TestDoneStampsXattr(t *testing.T) {
	for _, on := range []bool{true, false} {
		r := newRig(t)
		r.m.s.Privacy.StampOriginXattr = on
		payload := filepath.Join(r.dir, "a.iso")
		ref := "http://u:pw@example.com/a.iso"
		j := finish(t, r, AddParams{URIs: []string{ref}, Out: "a.iso"}, "a.iso")
		if err := unix.Setxattr(payload, "user.probe", []byte("1"), 0); err != nil {
			t.Skipf("no user xattrs here: %v", err)
		}
		if j.State != store.Done {
			t.Fatalf("state %s", j.State)
		}
		got := origin(payload)
		if on && got != "http://example.com/a.iso" {
			t.Fatalf("origin %q", got)
		}
		if !on && got != "" {
			t.Fatalf("stamped with setting off: %q", got)
		}
	}
}

func TestDoneStampFailureIsNotFatal(t *testing.T) {
	r := newRig(t)
	r.m.s.Privacy.StampOriginXattr = true
	// payload missing: stamping fails, the job still ends Done.
	j := finish(t, r, AddParams{URIs: []string{"http://example.com/gone.iso"}, Out: "gone.iso"}, "")
	if j.State != store.Done {
		t.Fatalf("state %s", j.State)
	}
}
