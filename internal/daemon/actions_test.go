package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

type recordFiles struct{ got []string }

func (f *recordFiles) RemoveFiles(_ context.Context, j *store.Job, _ []string) ([]trash.Entry, error) {
	f.got = append(f.got, j.ID)
	return nil, nil
}

func (f *recordFiles) Check(context.Context, *store.Job, []string) error { return nil }

func (f *recordFiles) RestoreFiles(context.Context, []trash.Entry) error { return nil }

func TestRemoveAndUndo(t *testing.T) {
	r := newRig(t)
	files := &recordFiles{}
	r.m.Files = files
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	tok, _, err := r.m.Remove(bg, []string{j.ID}, true)
	if err != nil || tok == "" {
		t.Fatalf("remove: %q %v", tok, err)
	}
	if got := r.get(t, j.ID); got.State != store.Removed || got.RemovedAt == nil {
		t.Fatalf("state %s", got.State)
	}
	if len(r.e.removed) != 1 || len(files.got) != 1 {
		t.Fatalf("engine removes %v, file removes %v", r.e.removed, files.got)
	}
	if err := r.m.Undo(bg, tok); err != nil {
		t.Fatal(err)
	}
	got := r.get(t, j.ID)
	if got.State != store.Paused || got.RemovedAt != nil || got.EngineRef != nil {
		t.Fatalf("after undo: %s ref %v", got.State, got.EngineRef)
	}
	if err := r.m.Undo(bg, tok); err == nil {
		t.Fatal("undo twice")
	}
	tok, _, _ = r.m.Remove(bg, []string{j.ID}, false)
	r.clk.add(11 * time.Second)
	if err := r.m.Undo(bg, tok); err == nil {
		t.Fatal("undo after the window")
	}
}

func TestStopSeeding(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{Torrent: []byte("d4:infod4:name1:xee")})
	stageFiles(t, j, map[string]string{"x": "x"}) // torrents finish in staging
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.Seeder = "active", true })
	r.event(ref, engine.KindBTComplete)
	if err := r.m.StopSeeding(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	if got := r.get(t, j.ID); got.State != store.Done {
		t.Fatalf("state %s", got.State)
	}
}

func TestRefreshErroredJob(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.ErrorCode, s.HTTPStatus = "error", 22, 403 })
	r.event(ref, engine.KindError)
	got, err := r.m.RefreshURI(bg, j.ID, "https://u:p@mirror.example.com/a.iso?t=2")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.Queued || got.ErrorKind != nil || got.URIs[0] != "https://mirror.example.com/a.iso?t=2" {
		t.Fatalf("after refresh: %s %v %v", got.State, got.ErrorKind, got.URIs)
	}
	if got.Options["http-user"] != "u" {
		t.Fatal("refresh credentials not kept for re-adds")
	}
	if _, err := r.m.RefreshURI(bg, j.ID, "file:///etc/passwd"); err == nil {
		t.Fatal("accepted a file: link")
	}
}

// A refresh that does not fit the job is a bad request, whatever the reason,
// so the CLI exits with the usage code for each.
func TestRefreshMisfitIsBadRequest(t *testing.T) {
	r := newRig(t)
	torrent := r.insert(t, store.Paused, "", func(j *store.Job) { j.Kind = "torrent" })
	done := r.insert(t, store.Done, "", nil)
	removed := r.insert(t, store.Removed, "", nil)
	http := r.insert(t, store.Paused, "", nil)
	for _, c := range []struct{ id, link string }{
		{torrent.ID, "https://example.com/f.bin"},
		{done.ID, "https://example.com/f.bin"},
		{removed.ID, "https://example.com/f.bin"},
		{http.ID, "magnet:?xt=urn:btih:abc"},
		{http.ID, "file:///etc/passwd"},
	} {
		_, err := r.m.RefreshURI(bg, c.id, c.link)
		if _, ok := errors.AsType[*BadRequestError](err); !ok {
			t.Errorf("refresh %s with %s: err %v, want a *BadRequestError", c.id, c.link, err)
		}
	}
	if _, err := r.m.RefreshURI(bg, torrent.ID, "https://example.com/f.bin"); !errors.Is(err, engine.ErrNotSupported) {
		t.Errorf("torrent refresh: err %v, want it to wrap engine.ErrNotSupported", err)
	}
}

func TestPauseHoldsWhileEngineSettles(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	if err := r.m.Pause(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	r.e.setState(ref, "active") // aria2 still finishing the pause
	r.event(ref, engine.KindStart)
	if got := r.get(t, j.ID); got.State != store.Paused {
		t.Fatalf("state %s, want paused", got.State)
	}
}

func TestRefreshNewRef(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.e.changeRef = "00000000000000cc" // aria2 refused the old gid on re-add
	got, err := r.m.RefreshURI(bg, j.ID, "http://mirror.example.com/a.iso")
	if err != nil || *got.EngineRef != "00000000000000cc" {
		t.Fatalf("ref %v err %v", got.EngineRef, err)
	}
	if adds, _ := r.e.count(); adds != 1 {
		t.Fatalf("adds %d, want 1", adds)
	}
}

func TestRefreshLostDownloadReadds(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.Remove(bg, ref) // the engine lost it
	r.e.changeErr = errors.New("is not found")
	got, err := r.m.RefreshURI(bg, j.ID, "http://mirror.example.com/a.iso")
	if err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 2 || r.e.adds[1].URIs[0] != "http://mirror.example.com/a.iso" {
		t.Fatalf("adds %d %+v", adds, r.e.adds)
	}
	if *got.EngineRef != string(ref) || got.State != store.Queued {
		t.Fatalf("ref %s state %s", *got.EngineRef, got.State)
	}
}

// A refresh of a paused job removes the download and adds it again. When
// that add fails the job stays paused with the new link, and comes back on
// resume or reconcile; the failure is logged, not dropped.
func TestRefreshPausedReaddFailsIsLogged(t *testing.T) {
	r := newRig(t)
	var logs bytes.Buffer
	r.m.log = slog.New(slog.NewTextHandler(&logs, nil))
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	if err := r.m.Pause(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	r.e.Remove(bg, r.ref(t, j.ID)) // the engine's remove went through
	r.e.changeErr = errors.New("re-add: refused")
	got, err := r.m.RefreshURI(bg, j.ID, "http://mirror.example.com/a.iso")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.Paused || got.URIs[0] != "http://mirror.example.com/a.iso" {
		t.Fatalf("state %s uris %v, want paused with the new link", got.State, got.URIs)
	}
	if !strings.Contains(logs.String(), "re-add: refused") {
		t.Fatalf("the engine's failure was not logged: %q", logs.String())
	}
	if err := r.m.Resume(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	if adds, _ := r.e.count(); adds != 2 || r.e.adds[1].URIs[0] != "http://mirror.example.com/a.iso" {
		t.Fatalf("resume did not add the job again from the new link: %d %+v", adds, r.e.adds)
	}
}

func TestRemoveCancelsRetry(t *testing.T) {
	r := newRig(t)
	r.m.s.Load().Retry.BackoffBase = "50ms"
	r.m.now = time.Now
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.fail429(t, j.ID)
	if _, _, err := r.m.Remove(bg, []string{j.ID}, false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if adds, _ := r.e.count(); adds != 1 {
		t.Fatalf("re-added after remove: %d adds", adds)
	}
	r.m.mu.Lock()
	n := len(r.m.retries)
	r.m.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d retry timers left", n)
	}
}

func TestPausedRowNeverAutoRetries(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, StartPaused: true})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.ErrorCode, s.HTTPStatus = "error", 22, 429 })
	r.event(ref, engine.KindError)
	if got := r.get(t, j.ID); got.State != store.Error {
		t.Fatalf("state %s, want error", got.State)
	}
}

func TestBatchSkipsAfterEngineDown(t *testing.T) {
	r := newRig(t)
	var ids []string
	for _, u := range []string{"a", "b", "c"} {
		j := r.add(t, AddParams{URIs: []string{"http://example.com/" + u}})
		ids = append(ids, j.ID)
	}
	down := &pauseDown{fakeEngine: r.e}
	r.m.e = down
	err := r.m.Pause(bg, ids)
	if !errors.Is(err, engine.ErrNotRunning) || down.pauses != 1 || !strings.Contains(err.Error(), "skipped") {
		t.Fatalf("err %v, pauses tried %d", err, down.pauses)
	}
}

// pauseDown fails pauses as if aria2 went away; status reads still work.
type pauseDown struct {
	*fakeEngine
	pauses int
}

func (p *pauseDown) Pause(context.Context, engine.EngineRef) error {
	p.pauses++
	return engine.ErrNotRunning
}

// A download that starts over because its .aria2 control file is gone
// leaves its old file behind; the daemon log names it.
func TestResumeLogsFileLeftBehind(t *testing.T) {
	r := newRig(t)
	var logs bytes.Buffer
	r.m.log = slog.New(slog.NewTextHandler(&logs, nil))
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	if err := r.m.Pause(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	r.e.restarted = "/dl/a.iso"
	if err := r.m.Resume(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "path=/dl/a.iso") {
		t.Fatalf("the old file was not logged: %q", logs.String())
	}
}
