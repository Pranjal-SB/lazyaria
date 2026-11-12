package daemon

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// liveDaemon is a manager on a real supervised aria2c.
type liveDaemon struct {
	m      *Manager
	e      engine.Engine
	cancel context.CancelFunc
	done   chan struct{}
}

func startLive(t *testing.T, st *store.Store, root string, tweak func(*config.Settings), setup func(*Manager)) *liveDaemon {
	t.Helper()
	s, _, _, err := config.Load("", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.BT.EnableDHT = false
	if tweak != nil {
		tweak(s)
	}
	p := config.Paths{StateDir: filepath.Join(root, "state"), RuntimeDir: filepath.Join(root, "run"), DownloadDir: filepath.Join(root, "dl")}
	p.AriaConf = filepath.Join(p.RuntimeDir, "aria2.conf")
	p.DHT = filepath.Join(p.StateDir, "dht.dat")
	for _, d := range []string{p.StateDir, p.RuntimeDir, p.DownloadDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	s.Downloads.Dir = p.DownloadDir
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := engine.NewAria2(supervisor.New(s, p, log))
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	d := &liveDaemon{m: NewManager(e, st, s, NewBus(512), log), e: e, done: make(chan struct{})}
	if setup != nil {
		setup(d.m)
	}
	var runCtx context.Context
	runCtx, d.cancel = context.WithCancel(bg)
	go func() { d.m.Run(runCtx); close(d.done) }()
	return d
}

func (d *liveDaemon) stop(t *testing.T) {
	t.Helper()
	d.cancel()
	<-d.done
	ctx, cancel := context.WithTimeout(bg, 15*time.Second)
	defer cancel()
	if err := d.e.Shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func waitJob(t *testing.T, st *store.Store, id string, d time.Duration, ok func(*store.Job) bool) *store.Job {
	t.Helper()
	end := time.Now().Add(d)
	for {
		j, err := st.GetJob(bg, id)
		if err != nil {
			t.Fatal(err)
		}
		if ok(j) {
			return j
		}
		if time.Now().After(end) {
			t.Fatalf("job never matched; last state %s kind %v msg %v", j.State, j.ErrorKind, j.ErrorMsg)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUnknownSize_NoPrecheck_RestartFromZero(t *testing.T) {
	testutil.RequireAria2(t)
	data := testutil.RandBytes(2 << 20)
	srv := testutil.NewFaultServer(t, map[string][]byte{"norange.bin": data})
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	slow := func(s *config.Settings) { s.Limits.GlobalDown = "512K" }

	// Any known size would be refused, so finishing proves no check ran.
	tiny := func(m *Manager) { m.free = func(string) (uint64, error) { return 1, nil } }
	d := startLive(t, st, root, slow, tiny)
	j, err := d.m.Add(bg, AddParams{URIs: []string{srv.URL("norange.bin", testutil.ModeNoRange)}})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, st, j.ID, 10*time.Second, func(j *store.Job) bool { return srv.Served("norange.bin") > 256<<10 })
	if got, _ := st.GetJob(bg, j.ID); got.State == store.Paused || got.State == store.Done {
		t.Fatalf("state %s before restart", got.State)
	}
	d.stop(t)

	d = startLive(t, st, root, nil, tiny)
	defer d.stop(t)
	got := waitJob(t, st, j.ID, 30*time.Second, func(j *store.Job) bool { return j.State == store.Done || j.State == store.Error })
	if got.State != store.Done {
		t.Fatalf("state %s kind %v msg %v", got.State, got.ErrorKind, got.ErrorMsg)
	}
	if !eventSeen(t, st, j.ID, "readded") {
		t.Fatal("restart did not re-add the job")
	}
	if n := srv.Served("norange.bin"); n <= int64(len(data)) {
		t.Fatalf("served %d bytes; the download did not start over", n)
	}
	if *got.Out != "norange.bin" {
		t.Fatalf("finished as %s, want norange.bin restarted in place", *got.Out)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "norange.1.bin")); err == nil {
		t.Fatal("restart wrote norange.1.bin beside the partial file")
	}
	path := filepath.Join(got.Dir, *got.Out)
	want := testutil.SHA256File(t, writeFile(t, data))
	if testutil.SHA256File(t, path) != want {
		t.Fatalf("sha mismatch for %s", path)
	}
}

func eventSeen(t *testing.T, st *store.Store, id, kind string) bool {
	t.Helper()
	evs, err := st.Events(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A file that appears between the on_exists check and aria2's start, under
// a name taken from the URL (no --out), is renamed beside by aria2 and
// never written into; the job follows the new name (spec G7).
func TestFreshAddNeverOverwritesLateFile(t *testing.T) {
	testutil.RequireAria2(t)
	data := testutil.RandBytes(256 << 10)
	srv := testutil.NewFaultServer(t, map[string][]byte{"late.bin": data})
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := []byte("the user's own file\n")
	orig := filepath.Join(root, "dl", "late.bin")
	var once sync.Once
	late := func(m *Manager) {
		dev := m.dev
		m.dev = func(dir string) (uint64, error) { // runs after the check, before the engine add
			once.Do(func() {
				if err := os.WriteFile(orig, old, 0o600); err != nil {
					t.Error(err)
				}
			})
			return dev(dir)
		}
	}
	d := startLive(t, st, root, nil, late)
	defer d.stop(t)
	j, err := d.m.Add(bg, AddParams{URIs: []string{srv.URL("late.bin", testutil.ModeOK)}})
	if err != nil {
		t.Fatal(err)
	}
	if j.Out != nil {
		t.Fatalf("out %q set by the daemon; this case is aria2's rename", *j.Out)
	}
	got := waitJob(t, st, j.ID, 30*time.Second, func(j *store.Job) bool { return j.State == store.Done || j.State == store.Error })
	if got.State != store.Done {
		t.Fatalf("state %s kind %v msg %v", got.State, got.ErrorKind, got.ErrorMsg)
	}
	if got.Name != "late.1.bin" || got.Out == nil || *got.Out != got.Name {
		t.Fatalf("name %q out %v; want the renamed file tracked", got.Name, got.Out)
	}
	if b, _ := os.ReadFile(orig); string(b) != string(old) {
		t.Fatalf("the existing file was changed (now %d bytes)", len(b))
	}
	if testutil.SHA256File(t, filepath.Join(got.Dir, got.Name)) != testutil.SHA256File(t, writeFile(t, data)) {
		t.Fatal("sha mismatch")
	}
}

// A server-named job whose first attempt failed before any response never
// learned its path; when the retry names an existing user file, the re-add
// must rename beside it, not resume into it.
func TestRetryNeverResumesIntoUnownedFile(t *testing.T) {
	testutil.RequireAria2(t)
	data := testutil.RandBytes(256 << 10)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="report.pdf"`)
		if hits.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		http.ServeContent(w, r, "report.pdf", time.Time{}, bytes.NewReader(data))
	}))
	defer srv.Close()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := []byte("the user's own report\n")
	orig := filepath.Join(root, "dl", "report.pdf")
	retry := func(s *config.Settings) {
		s.Retry.MaxTries = 1 // aria2 gives up at once; the daemon's retry re-adds
		s.Retry.AutoRetry = []string{"503"}
		s.Retry.BackoffBase = "1s"
	}
	d := startLive(t, st, root, retry, func(*Manager) {
		if err := os.WriteFile(orig, old, 0o600); err != nil {
			t.Fatal(err)
		}
	})
	defer d.stop(t)
	j, err := d.m.Add(bg, AddParams{URIs: []string{srv.URL + "/get"}})
	if err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, st, j.ID, 30*time.Second, func(j *store.Job) bool { return j.State == store.Done })
	if hits.Load() < 2 || !eventSeen(t, st, j.ID, "retry") {
		t.Fatalf("no re-add happened (%d requests)", hits.Load())
	}
	if b, _ := os.ReadFile(orig); string(b) != string(old) {
		t.Fatalf("the existing file was changed (now %d bytes)", len(b))
	}
	if got.Name != "report.1.pdf" || got.Out == nil || *got.Out != got.Name {
		t.Fatalf("name %q out %v; want report.1.pdf tracked", got.Name, got.Out)
	}
	if testutil.SHA256File(t, filepath.Join(got.Dir, got.Name)) != testutil.SHA256File(t, writeFile(t, data)) {
		t.Fatal("sha mismatch")
	}
}
