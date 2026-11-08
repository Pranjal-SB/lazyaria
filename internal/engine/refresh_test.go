package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

func TestChangeURIOnPaused(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"old.bin": data, "new.bin": data})
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("old.bin", testutil.ModeSlow)}})
	waitStatus(t, e, ref, 5*time.Second, func(s EngineStatus) bool { return s.Done > 0 })
	if err := e.Pause(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, ref, 5*time.Second, func(s EngineStatus) bool { return s.State == "paused" })

	got, err := e.ChangeURI(ctxT(t, 10*time.Second), ref, fs.URL("new.bin", testutil.ModeOK))
	if err != nil {
		t.Fatal(err)
	}
	if got != ref {
		t.Fatalf("ref changed: %s -> %s", ref, got)
	}
	if st := status(t, e, ref); st.State != "paused" {
		t.Fatalf("a user-paused job must stay paused after refresh, got %s", st.State)
	}
	if err := e.Resume(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, ref, KindComplete, 20*time.Second)
	checkSHA(t, status(t, e, ref).Path)
	if fs.Hits("new.bin") == 0 {
		t.Fatal("new uri was never fetched")
	}
}

func TestChangeURIOnActive(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"old.bin": data, "new.bin": data})
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("old.bin", testutil.ModeSlow)}})
	waitStatus(t, e, ref, 5*time.Second, func(s EngineStatus) bool { return s.Done > 0 })
	got, err := e.ChangeURI(ctxT(t, 10*time.Second), ref, fs.URL("new.bin", testutil.ModeOK))
	if err != nil || got != ref {
		t.Fatalf("ref %s err %v", got, err)
	}
	waitEvent(t, e, ref, KindComplete, 20*time.Second)
	checkSHA(t, status(t, e, ref).Path)
	if fs.Hits("new.bin") == 0 {
		t.Fatal("new uri was never fetched")
	}
}

func TestReaddKeepsGID(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"g.bin": data})
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("g.bin", testutil.Mode403)}})
	waitEvent(t, e, ref, KindError, 10*time.Second)
	got, err := e.ChangeURI(ctxT(t, 10*time.Second), ref, fs.URL("g.bin", testutil.ModeOK))
	if err != nil {
		t.Fatal(err)
	}
	if got != ref {
		t.Fatalf("re-add changed the ref: %s -> %s", ref, got)
	}
	waitEvent(t, e, ref, KindComplete, 20*time.Second)
	checkSHA(t, status(t, e, ref).Path)
}

func TestChangeURIOnError(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"e.bin": data, "fresh.bin": data})
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("e.bin", testutil.ModeSlow)}})
	// Get past the expire limit, then pause so the next request is a new one.
	waitStatus(t, e, ref, 10*time.Second, func(s EngineStatus) bool { return s.Done > 384<<10 })
	if err := e.Pause(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, ref, 5*time.Second, func(s EngineStatus) bool { return s.State == "paused" })
	fs.SetMode("e.bin", testutil.ModeExpire)
	if err := e.Resume(ctxT(t, 5*time.Second), ref); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, ref, KindError, 10*time.Second)
	st := status(t, e, ref)
	// aria2 reports "No URI available." rather than the 403 itself when a
	// resumed download fails, so only the code is checked here.
	if st.ErrorCode != 22 || st.Done == 0 {
		t.Fatalf("want a partial code-22 error, got %+v", st)
	}

	got, err := e.ChangeURI(ctxT(t, 10*time.Second), ref, fs.URL("fresh.bin", testutil.ModeOK))
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, got, KindComplete, 20*time.Second)
	end := status(t, e, got)
	if end.Path != st.Path {
		t.Fatalf("re-add wrote %s, want %s", end.Path, st.Path)
	}
	checkSHA(t, end.Path)
	checkResumed(t, fs, "fresh.bin", st.Done)
}

// TestChangeURIAfterQuickError refreshes a download that failed mid-stream
// without a pause, seconds after it started and so well inside
// auto-save-interval. The file is above aria2's 5 MiB
// no-file-allocation-limit, so it is preallocated to full length.
func TestChangeURIAfterQuickError(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	big := testutil.RandBytes(8 << 20)
	fs := testutil.NewFaultServer(t, map[string][]byte{"q.bin": big, "fresh.bin": big})
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("q.bin", testutil.ModeSlow)}})
	waitStatus(t, e, ref, 10*time.Second, func(s EngineStatus) bool { return s.Done > 256<<10 })
	// The link expires and the open connection dies with it.
	fs.SetMode("q.bin", testutil.Mode403)
	fs.DropConnections()
	waitEvent(t, e, ref, KindError, 10*time.Second)
	st := status(t, e, ref)
	if st.Done == 0 {
		t.Fatalf("want a partial error, got %+v", st)
	}

	got, err := e.ChangeURI(ctxT(t, 10*time.Second), ref, fs.URL("fresh.bin", testutil.ModeOK))
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, got, KindComplete, 20*time.Second)
	end := status(t, e, got)
	if end.Path != st.Path {
		t.Fatalf("re-add wrote %s, want %s", end.Path, st.Path)
	}
	if testutil.SHA256File(t, end.Path) != testutil.SHA256File(t, writeTemp(t, big)) {
		t.Fatalf("sha mismatch for %s", end.Path)
	}
	checkResumed(t, fs, "fresh.bin", st.Done)
}

// checkResumed fails unless aria2 asked name for a range starting past zero
// but within the done bytes already on disk, which only a resume from the
// .aria2 control file does. Served byte counts cannot show this: aria2 first
// sends an unranged GET and drops it once the control file shows data on
// disk, and the server counts whatever it pushed into that socket before the
// close, which varies with timing. A restart cannot pass: its range starts
// are split points at least min-split-size apart, and aria2's minimum is 1 MiB.
func checkResumed(t *testing.T, fs *testutil.FaultServer, name string, done int64) {
	t.Helper()
	if done >= 1<<20 {
		t.Fatalf("%d bytes on disk; the check needs less than 1 MiB", done)
	}
	ranges := fs.Ranges(name)
	for _, r := range ranges {
		var start int64
		if _, err := fmt.Sscanf(r, "bytes=%d-", &start); err == nil && start > 0 && start <= done {
			return
		}
	}
	t.Fatalf("%s: no request resumed within the %d bytes on disk: restarted, not resumed; ranges %q", name, done, ranges)
}
