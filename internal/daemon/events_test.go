package daemon

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		code, http int
		msg        string
		done       int64
		detailHTTP int
		state      store.State
		kind       string
	}{
		{"429", 22, 429, "", 0, 0, store.RetryWait, "rate-limited"},
		{"403", 22, 403, "", 0, 0, store.Error, "link-expired"},
		{"410", 22, 410, "", 0, 0, store.Error, "link-expired"},
		{"500", 22, 500, "", 0, 0, store.Error, "http-500"},
		{"not found", 3, 0, "", 0, 0, store.Error, "not-found"},
		{"auth", 24, 0, "", 0, 0, store.Error, "auth"},
		{"busy", 29, 0, "", 0, 0, store.Error, "server-busy"},
		{"exists", 13, 0, "", 0, 0, store.Error, "exists"},
		{"timeout", 2, 0, "", 0, 0, store.Error, "network"},
		{"network", 6, 0, "", 0, 0, store.Error, "network"},
		{"dns", 19, 0, "", 0, 0, store.Error, "network"},
		{"unfinished", 7, 0, "", 0, 0, store.Active, ""},
		{"other", 32, 0, "", 0, 0, store.Error, "aria2-32"},
		{"refused on fresh add", 1, 0, "No URI available.", 0, 0, store.Error, "network"},
		{"hidden status after resume", 1, 0, "No URI available.", 500, 0, store.Error, "http-unknown"},
		{"hidden 22", 22, 0, "", 500, 0, store.Error, "http-unknown"},
		{"status from details", 22, 0, "", 500, 403, store.Error, "link-expired"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
			ref := r.ref(t, j.ID)
			r.e.set(ref, func(s *engine.EngineStatus) { s.State = "active" })
			r.event(ref, engine.KindStart)
			r.e.set(ref, func(s *engine.EngineStatus) {
				s.State, s.ErrorCode, s.HTTPStatus, s.ErrorMsg, s.Done = "error", c.code, c.http, c.msg, c.done
			})
			if c.detailHTTP != 0 {
				r.e.details[ref] = engine.EngineDetails{Status: engine.EngineStatus{HTTPStatus: c.detailHTTP}}
			}
			r.event(ref, engine.KindError)
			got := r.get(t, j.ID)
			if got.State != c.state {
				t.Fatalf("state %s, want %s", got.State, c.state)
			}
			if c.kind != "" && (got.ErrorKind == nil || *got.ErrorKind != c.kind) {
				t.Fatalf("kind %v, want %s", got.ErrorKind, c.kind)
			}
			if c.state == store.RetryWait {
				want := r.clk.now().Add(30 * time.Second)
				if got.RetryAt == nil || got.RetryAt.Sub(want).Abs() > time.Millisecond {
					t.Fatalf("retry_at %v, want %v", got.RetryAt, want)
				}
			}
		})
	}
}

func TestNetworkAutoRetry(t *testing.T) {
	r := newRig(t)
	r.m.s.Load().Retry.AutoRetry = []string{"429", "network"}
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.ErrorCode, s.ErrorMsg = "error", 1, "No URI available." })
	r.event(ref, engine.KindError)
	if got := r.get(t, j.ID); got.State != store.RetryWait {
		t.Fatalf("state %s, want retry-wait", got.State)
	}
}

func TestDiskFullPausesSameDir(t *testing.T) {
	r := newRig(t)
	a := r.add(t, AddParams{URIs: []string{"http://example.com/a"}})
	b := r.add(t, AddParams{URIs: []string{"http://example.com/b"}})
	other := r.add(t, AddParams{URIs: []string{"http://example.com/c"}, Dir: t.TempDir()})
	for _, j := range []*store.Job{a, b, other} {
		ref := r.ref(t, j.ID)
		r.e.setState(ref, "active")
		r.event(ref, engine.KindStart)
	}
	ra := r.ref(t, a.ID)
	r.e.set(ra, func(s *engine.EngineStatus) { s.State, s.ErrorCode = "error", 9 })
	r.event(ra, engine.KindError)
	if got := r.get(t, a.ID); got.State != store.Error || *got.ErrorKind != "disk-full" {
		t.Fatalf("a: %s %v", got.State, got.ErrorKind)
	}
	if got := r.get(t, b.ID); got.State != store.Paused {
		t.Fatalf("b in same dir: %s, want paused", got.State)
	}
	if got := r.get(t, other.ID); got.State != store.Active {
		t.Fatalf("other dir: %s, want active", got.State)
	}
}

func TestSeedingState(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{Torrent: []byte("d4:infod4:name1:xee")})
	if r.e.adds[0].Options["seed-time"] != "0" {
		t.Fatalf("seed-time %q, want 0", r.e.adds[0].Options["seed-time"])
	}
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.IsBT = "active", true })
	r.event(ref, engine.KindStart)
	r.event(ref, engine.KindBTComplete)
	if got := r.get(t, j.ID); got.State != store.Seeding {
		t.Fatalf("after bt-complete: %s", got.State)
	}
	r.e.setState(ref, "complete")
	r.event(ref, engine.KindComplete)
	got := r.get(t, j.ID)
	if got.State != store.Done || got.FinishedAt == nil {
		t.Fatalf("after complete: %s", got.State)
	}
}

func TestSeederStatusMeansSeeding(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{Torrent: []byte("d4:infod4:name1:xee")})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.Seeder = "active", true })
	r.event(ref, engine.KindStart) // the bt-complete notification was missed
	if got := r.get(t, j.ID); got.State != store.Seeding {
		t.Fatalf("state %s, want seeding", got.State)
	}
}

func TestAutoRenameTracked(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/file.zip"}, Out: "file.zip"})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.Path = "complete", r.dir+"/file.1.zip" })
	r.event(ref, engine.KindComplete)
	got := r.get(t, j.ID)
	if got.Out == nil || *got.Out != "file.1.zip" || got.Name != "file.1.zip" {
		t.Fatalf("out %v name %q", got.Out, got.Name)
	}
	if got.State != store.Done {
		t.Fatalf("state %s", got.State)
	}
	if !r.hasEvent(t, j.ID, "renamed") {
		t.Fatal("no renamed event")
	}
}

func TestMagnetAdoption(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"magnet:?xt=urn:btih:abc&dn=Linux"}})
	if j.Kind != "magnet" || j.Name != "Linux" {
		t.Fatalf("kind %q name %q", j.Kind, j.Name)
	}
	parent := r.ref(t, j.ID)
	r.e.set("X", func(s *engine.EngineStatus) { s.State, s.IsBT = "active", true })
	r.e.set(parent, func(s *engine.EngineStatus) { s.State, s.FollowedBy = "complete", "X" })
	r.event(parent, engine.KindComplete)
	got := r.get(t, j.ID)
	if *got.EngineRef != "X" || got.State == store.Done {
		t.Fatalf("ref %s state %s", *got.EngineRef, got.State)
	}
	if got.Dir != r.dir {
		t.Fatalf("dir changed: %s", got.Dir)
	}
	if len(r.e.removed) != 1 || r.e.removed[0] != parent {
		t.Fatalf("parent not removed: %v", r.e.removed)
	}
}

func TestExistsHints(t *testing.T) {
	for _, c := range []struct {
		code      int
		want, not string
	}{
		{13, "Move or rename the existing file", "Another download"},
		{11, "Another download in aria2 is writing the same file", "Move or rename"},
	} {
		var x store.Job
		setErr(&x, "exists", engine.EngineStatus{ErrorCode: c.code, ErrorMsg: "File /d/a.bin exists."})
		if msg := *x.ErrorMsg; !strings.Contains(msg, c.want) || strings.Contains(msg, c.not) {
			t.Errorf("code %d: %q", c.code, msg)
		}
	}
}

// A retry that fails before any reply reports 0 bytes; the job keeps the
// count, and with it ownership of its file. A new file resets it.
func TestDoneBytesKeepOwnership(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.bin"}, Out: "a.bin"})
	path := filepath.Join(r.dir, "a.bin")
	j = r.m.apply(bg, j, engine.EngineStatus{State: "active", Done: 100, Total: 1000, Path: path}, "")
	if !ownsFile(j) {
		t.Fatalf("done %v: not owned after writing", j.DoneBytes)
	}
	j = r.m.apply(bg, j, engine.EngineStatus{State: "error", ErrorCode: 22, HTTPStatus: 503, Path: path}, "")
	if got := r.get(t, j.ID); got.DoneBytes == nil || *got.DoneBytes != 100 {
		t.Fatalf("after a failed retry done %v, want 100", got.DoneBytes)
	}
	j = r.get(t, j.ID)
	j.State = store.Active
	j = r.m.trackName(bg, j, engine.EngineStatus{Path: filepath.Join(r.dir, "a.1.bin")})
	if ownsFile(j) || *j.Out != "a.1.bin" {
		t.Fatalf("after a rename out %q done %v", *j.Out, *j.DoneBytes)
	}
}
