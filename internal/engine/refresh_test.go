package engine

import (
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
	total := fs.Served("e.bin") + fs.Served("fresh.bin")
	if total >= 2*fileSize {
		t.Fatalf("fetched %d bytes in total, want < %d", total, 2*fileSize)
	}
	// A restart would refetch everything; a resume skips what was on disk.
	if fresh := fs.Served("fresh.bin"); fresh > fileSize-st.Done/2 {
		t.Fatalf("refresh fetched %d bytes with %d already on disk: restarted, not resumed", fresh, st.Done)
	}
}
