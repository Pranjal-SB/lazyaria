package engine

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

func TestAddDownloadsAndCompletes(t *testing.T) {
	t.Parallel()
	e, dir := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"a.bin": data})
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("a.bin", testutil.ModeOK)}})
	waitEvent(t, e, ref, KindComplete, 20*time.Second)
	st := status(t, e, ref)
	if st.State != "complete" || st.Name != "a.bin" || st.Path != filepath.Join(dir, "a.bin") || st.IsBT {
		t.Fatalf("status %+v", st)
	}
	checkSHA(t, st.Path)
}

func TestAdd_DirWithSpacesAndUnicode(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"u.bin": data})
	dir := filepath.Join(t.TempDir(), "my files", "ünï")
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("u.bin", testutil.ModeOK)}, Dir: dir})
	waitEvent(t, e, ref, KindComplete, 20*time.Second)
	checkSHA(t, filepath.Join(dir, "u.bin"))
}

func TestPauseResume(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"s.bin": data})
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("s.bin", testutil.ModeSlow)}})
	waitStatus(t, e, ref, 5*time.Second, func(s EngineStatus) bool { return s.Done > 0 })
	if err := e.Pause(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, ref, 5*time.Second, func(s EngineStatus) bool { return s.State == "paused" })
	if err := e.Pause(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatalf("second pause should be a no-op: %v", err)
	}
	if err := e.Resume(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, ref, 5*time.Second, func(s EngineStatus) bool { return s.State == "active" })
	if err := e.Remove(ctxT(t, 10*time.Second), ref); err != nil {
		t.Fatal(err)
	}
	if st := status(t, e, ref); st.State != "removed" || st.Known {
		t.Fatalf("after remove: %+v", st)
	}
}

// A plain resume of a paused download whose .aria2 control file is gone
// has the same hazard as a refresh (see TestChangeURIOnPausedWithoutControlFile).
func TestResumeWithoutControlFile(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	big := testutil.RandBytes(16 << 20) // above aria2's 5 MiB no-file-allocation-limit
	fs := testutil.NewFaultServer(t, map[string][]byte{"r.bin": big})
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("r.bin", testutil.ModeOK)}, Options: map[string]string{"max-download-limit": "2M"}})
	waitStatus(t, e, ref, 15*time.Second, func(s EngineStatus) bool { return s.Done > 1<<20 })
	if err := e.Pause(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatal(err)
	}
	paused := waitStatus(t, e, ref, 5*time.Second, func(s EngineStatus) bool { return s.State == "paused" })
	if err := os.Remove(paused.Path + ".aria2"); err != nil {
		t.Fatal(err)
	}
	rawCall(t, e, "aria2.changeOption", nil, string(ref), map[string]string{"max-download-limit": "0"})
	if err := e.Resume(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatal(err)
	}
	checkPayload(t, e, ref, big)
}

func TestRejectsDisallowedOption(t *testing.T) {
	e := NewAria2Attach("ws://127.0.0.1:1/jsonrpc", "")
	_, err := e.Add(ctxT(t, time.Second), AddRequest{
		URIs:    []string{"http://example.com/f"},
		Options: map[string]string{"on-download-complete": "x"},
	})
	var inv ErrInvalidOption
	if !errors.As(err, &inv) || inv.Name != "on-download-complete" {
		t.Fatalf("got %v", err)
	}
}

func TestUserinfoNotInURI(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"c.bin": data})
	uri := strings.Replace(fs.URL("c.bin", testutil.ModeOK), "http://", "http://u:p@", 1)
	ref := add(t, e, AddRequest{URIs: []string{uri}, Paused: true})
	var uris []struct{ URI string }
	rawCall(t, e, "aria2.getUris", &uris, string(ref))
	if len(uris) == 0 {
		t.Fatal("no uris")
	}
	for _, u := range uris {
		if strings.Contains(u.URI, "u:p@") || strings.Contains(u.URI, "@") {
			t.Fatalf("credentials leaked into uri %q", u.URI)
		}
	}
	var opt map[string]string
	rawCall(t, e, "aria2.getOption", &opt, string(ref))
	if opt["http-user"] != "u" || opt["http-passwd"] != "p" {
		t.Fatalf("auth options not set: user=%q", opt["http-user"])
	}
}

func Test403Is22WithStatus(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"x.bin": data})
	url := fs.URL("x.bin", testutil.ModeExpire)
	// One full fetch uses up the link, so aria2's request gets 403.
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	ref := add(t, e, AddRequest{URIs: []string{url}})
	waitEvent(t, e, ref, KindError, 10*time.Second)
	st := status(t, e, ref)
	if st.State != "error" || st.ErrorCode != 22 || st.HTTPStatus != 403 {
		t.Fatalf("status %+v", st)
	}
}

func TestLimits(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"l.bin": data})
	if err := e.SetGlobalLimit(ctxT(t, 5*time.Second), 64<<10, 0); err != nil {
		t.Fatal(err)
	}
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("l.bin", testutil.ModeOK)}})
	time.Sleep(time.Second) // aria2 lets ~128 KiB through in the first second
	// Done moves in 64 KiB steps, too coarse for a 2 s window, so average
	// aria2's own speed reading instead.
	var sum int64
	for range 8 {
		time.Sleep(250 * time.Millisecond)
		sum += status(t, e, ref).Down
	}
	if speed := sum / 8; speed > 80<<10 || speed <= 0 {
		t.Fatalf("speed %d B/s, want (0, %d]", speed, 80<<10)
	}
	if err := e.SetLimit(ctxT(t, 5*time.Second), ref, 32<<10, 0); err != nil {
		t.Fatal(err)
	}
	var opt map[string]string
	rawCall(t, e, "aria2.getOption", &opt, string(ref))
	if opt["max-download-limit"] != "32768" {
		t.Fatalf("per-job limit %q", opt["max-download-limit"])
	}
}

func TestSetGlobalOptions(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	if err := e.SetGlobalOptions(ctxT(t, 5*time.Second), map[string]string{"max-concurrent-downloads": "3", "split": "4"}); err != nil {
		t.Fatal(err)
	}
	var opt map[string]string
	rawCall(t, e, "aria2.getGlobalOption", &opt)
	if opt["max-concurrent-downloads"] != "3" || opt["split"] != "4" {
		t.Fatalf("global options max-concurrent-downloads=%q split=%q, want 3 and 4", opt["max-concurrent-downloads"], opt["split"])
	}
}
