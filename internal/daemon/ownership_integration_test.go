package daemon

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// reportServer names every reply report.pdf by Content-Disposition, answers
// the first request with 503 and then serves data.
func reportServer(t *testing.T, data []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	hits := new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="report.pdf"`)
		if hits.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		http.ServeContent(w, r, "report.pdf", time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

// ownershipCase adds link (with out), lets the first attempt fail with 503,
// puts the user's report.pdf in place, runs recover and checks the user's
// file is untouched and the job finished as report.1.pdf, tracked.
func ownershipCase(t *testing.T, out string, recover func(d *liveDaemon, j *store.Job, srvURL string) error) {
	testutil.RequireAria2(t)
	data := testutil.RandBytes(256 << 10)
	srv, hits := reportServer(t, data)
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	noRetry := func(s *config.Settings) { s.Retry.MaxTries, s.Retry.AutoRetry = 1, nil }
	d := startLive(t, st, root, noRetry, nil)
	defer d.stop(t)
	j, err := d.m.Add(bg, AddParams{URIs: []string{srv.URL + "/get"}, Out: out})
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, st, j.ID, 15*time.Second, func(j *store.Job) bool { return j.State == store.Error })
	old := []byte("the user's own report\n")
	orig := filepath.Join(j.Dir, "report.pdf")
	if err := os.WriteFile(orig, old, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := recover(d, j, srv.URL); err != nil {
		t.Fatal(err)
	}
	got := waitJob(t, st, j.ID, 30*time.Second, func(j *store.Job) bool { return j.State == store.Done || j.State == store.Error })
	if got.State != store.Done || hits.Load() < 2 {
		t.Fatalf("state %s (%v), %d requests", got.State, got.ErrorMsg, hits.Load())
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

// A refreshed link on a server-named job that never got a reply must not
// resume into the user's file the server names now (probe p1).
func TestRefreshNeverResumesIntoUnownedFile(t *testing.T) {
	ownershipCase(t, "", func(d *liveDaemon, j *store.Job, u string) error {
		_, err := d.m.RefreshURI(bg, j.ID, u+"/get2")
		return err
	})
}

// A job with --out that never wrote a byte does not own that name: a file
// the user puts there before the retry is renamed beside, not written into.
func TestRetryWithOutNeverWritesUnownedFile(t *testing.T) {
	ownershipCase(t, "report.pdf", func(d *liveDaemon, j *store.Job, _ string) error {
		return d.m.Resume(bg, []string{j.ID})
	})
}
