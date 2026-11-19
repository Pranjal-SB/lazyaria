package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

func wfile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func exists1(p string) bool { _, err := os.Lstat(p); return err == nil }

// wrote records that job id has written bytes, which makes its file its own.
func (r *rig) wrote(t *testing.T, id string) {
	t.Helper()
	if _, err := r.m.change(bg, id, "", "", func(x *store.Job) { n := int64(4); x.DoneBytes = &n }); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveUndo(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	payload := filepath.Join(stagingDir(j), "a.iso")
	wfile(t, payload, "data")
	r.wrote(t, j.ID)
	wfile(t, payload+".aria2", "ctl")

	tok, _, err := r.m.Remove(bg, []string{j.ID}, true)
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

	tok, _, err = r.m.Remove(bg, []string{j.ID}, true)
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
	r.m.Files = &TrashFiles{check: func(string) error { return trash.ErrNoTrash }}
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	before := r.get(t, j.ID)
	payload := filepath.Join(stagingDir(j), "a.iso")
	wfile(t, payload, "data")
	r.wrote(t, j.ID)
	tok, _, err := r.m.Remove(bg, []string{j.ID}, true)
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
	if len(r.e.removed) != 0 || len(r.e.paused) != 0 {
		t.Fatalf("engine touched: removed %v paused %v", r.e.removed, r.e.paused)
	}
	if _, ok := r.e.dl[ref]; !ok {
		t.Fatal("engine lost the download")
	}
	if got := r.get(t, j.ID); got.State != before.State || *got.EngineRef != *before.EngineRef {
		t.Fatalf("row changed: %s", got.State)
	}
}

func TestRemoveRenameFailureReaddsPaused(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	r.m.Files = &TrashFiles{trash: func(string) (trash.Entry, error) { return trash.Entry{}, trash.ErrNoTrash }}
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	payload := filepath.Join(stagingDir(j), "a.iso")
	wfile(t, payload, "data")
	r.wrote(t, j.ID)
	_, _, err := r.m.Remove(bg, []string{j.ID}, true)
	var nc *NeedsConfirmError
	if !errors.As(err, &nc) {
		t.Fatalf("want NeedsConfirmError, got %v", err)
	}
	got := r.get(t, j.ID)
	if got.State != store.Paused || got.EngineRef == nil {
		t.Fatalf("state %s ref %v", got.State, got.EngineRef)
	}
	if st, ok := r.e.dl[engine.EngineRef(*got.EngineRef)]; !ok || st.State != "paused" {
		t.Fatalf("engine has %v %v", ok, st.State)
	}
	if !exists1(payload) {
		t.Fatal("payload missing")
	}
}

func TestRemoveNeverTrashesFilesTheJobDoesNotOwn(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	// duplicate "download anyway": the user's original sits at the target.
	orig := filepath.Join(r.dir, "a.iso")
	wfile(t, orig, "mine")
	dup := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso", Force: true})
	// magnet whose metadata has not arrived.
	magnet := r.insert(t, store.Queued, "", func(j *store.Job) { j.Name = "download" })
	wfile(t, filepath.Join(r.dir, "download"), "other")
	// a name that cleans to ".".
	dot := r.insert(t, store.Queued, "", func(j *store.Job) { o := "sub/.."; j.Out = &o })
	if _, _, err := r.m.Remove(bg, []string{dup.ID, magnet.ID, dot.ID}, true); err != nil {
		t.Fatal(err)
	}
	if !exists1(orig) || !exists1(filepath.Join(r.dir, "download")) {
		t.Fatal("trashed a file the job does not own")
	}
	if !exists1(r.dir) {
		t.Fatal("dir gone")
	}
	if _, ok := payloadPath(&store.Job{Dir: r.dir, Name: "x", Out: func() *string { s := "../x"; return &s }()}); ok {
		t.Fatal("escaping name accepted")
	}
}

func TestUndoStaysRemovedWhenFilesCannotReturn(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	payload := filepath.Join(stagingDir(j), "a.iso")
	wfile(t, payload, "data")
	r.wrote(t, j.ID)
	tok, _, err := r.m.Remove(bg, []string{j.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	wfile(t, payload, "newcomer")
	err = r.m.Undo(bg, tok)
	if err == nil || !strings.Contains(err.Error(), j.ID) {
		t.Fatalf("want error naming the job, got %v", err)
	}
	if got := r.get(t, j.ID); got.State != store.Removed {
		t.Fatalf("state %s", got.State)
	}
	if b, _ := os.ReadFile(payload); string(b) != "newcomer" {
		t.Fatal("newcomer overwritten")
	}
	os.Remove(payload)
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if b, _ := os.ReadFile(payload); string(b) != "data" {
		t.Fatal("not restored on retry")
	}
	if got := r.get(t, j.ID); got.State != store.Paused {
		t.Fatalf("state %s", got.State)
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
	payload := filepath.Join(stagingDir(j), "a.iso")
	wfile(t, payload, "data")
	r.wrote(t, j.ID)
	wfile(t, payload+".aria2", "ctl")
	_, _, err := r.m.Remove(bg, []string{j.ID}, true)
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
		wfile(t, filepath.Join(stagingDir(j), file), "data")
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
		r.m.s.Load().Privacy.StampOriginXattr = on
		ref := "http://u:pw@example.com/a.iso"
		j := finish(t, r, AddParams{URIs: []string{ref}, Out: "a.iso"}, "a.iso")
		payload, _ := payloadPath(j)
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
	r.m.s.Load().Privacy.StampOriginXattr = true
	// A read-only payload refuses the xattr (unless run as root); the job
	// still ends Done.
	j := r.add(t, AddParams{URIs: []string{"http://example.com/ro.iso"}, Out: "ro.iso"})
	wfile(t, filepath.Join(stagingDir(j), "ro.iso"), "data")
	os.Chmod(filepath.Join(stagingDir(j), "ro.iso"), 0o400)
	if j = r.complete(t, j, "ro.iso"); j.State != store.Done {
		t.Fatalf("state %s", j.State)
	}
}

func TestDoneStampsReferrerWithoutUserinfo(t *testing.T) {
	r := newRig(t)
	r.m.s.Load().Privacy.StampOriginXattr = true
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	ref := "https://me:secret@ref.example/page"
	if _, err := r.st.UpdateJob(bg, j.ID, func(x *store.Job) error { x.Referrer = &ref; return nil }); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(stagingDir(j), "a.iso")
	wfile(t, payload, "data")
	eref := r.ref(t, j.ID)
	r.e.set(eref, func(s *engine.EngineStatus) { s.State = "complete" })
	r.event(eref, engine.KindComplete)
	payload, _ = payloadPath(r.get(t, j.ID))
	buf := make([]byte, 256)
	n, err := unix.Getxattr(payload, "user.xdg.referrer.url", buf)
	if err != nil {
		t.Skipf("no user xattrs here: %v", err)
	}
	if got := string(buf[:n]); got != "https://ref.example/page" {
		t.Fatalf("referrer %q", got)
	}
	if origin(payload) != "http://example.com/a.iso" {
		t.Fatalf("origin %q", origin(payload))
	}
}

// StartedAt is set before aria2 writes a byte. A job that failed before
// writing anything does not own the user's file at its name.
func TestRemoveDeleteFilesSparesUnwrittenName(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	started, none := r.clk.now(), int64(0)
	j := r.insert(t, store.Error, "", func(j *store.Job) { j.StartedAt, j.DoneBytes = &started, &none })
	user := filepath.Join(r.dir, "f.bin")
	wfile(t, user, "USER FILE")
	if _, _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(user); string(b) != "USER FILE" {
		t.Fatalf("the user's f.bin was trashed (now %q)", b)
	}
}

// Polling keeps a running job's byte count in memory only, so the row says
// 0 until the next state change. Removing it with its files must still take
// the partial payload, not just the control file.
func TestRemoveRunningTrashesPartial(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, Out: "a.iso"})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	payload := filepath.Join(stagingDir(j), "a.iso")
	wfile(t, payload, "data")
	wfile(t, payload+".aria2", "ctl")
	r.e.set(ref, func(s *engine.EngineStatus) { s.Done, s.Total = 4, 100 })
	r.m.poll(bg)
	if got := r.get(t, j.ID); got.DoneBytes != nil && *got.DoneBytes > 0 {
		t.Fatalf("poll stored %d bytes in the row; this test needs them in memory only", *got.DoneBytes)
	}

	if _, _, err := r.m.Remove(bg, []string{j.ID}, true); err != nil {
		t.Fatal(err)
	}
	if exists1(payload) || exists1(payload+".aria2") {
		t.Fatalf("left behind: payload %v, control file %v", exists1(payload), exists1(payload+".aria2"))
	}
}
