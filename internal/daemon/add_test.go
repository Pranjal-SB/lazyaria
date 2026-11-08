package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestAddIntentFirst(t *testing.T) {
	r := newRig(t)
	r.e.addEntered = make(chan struct{}, 1)
	r.e.addBlock = make(chan struct{})
	done := make(chan *store.Job, 1)
	go func() {
		j, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/a.iso"}, Dir: r.dir})
		if err != nil {
			t.Error(err)
		}
		done <- j
	}()
	<-r.e.addEntered
	js, err := r.st.ListJobs(bg, store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 1 || js[0].State != store.Queued || js[0].EngineRef != nil {
		t.Fatalf("before engine add: %+v", js)
	}
	close(r.e.addBlock)
	j := <-done
	if j.EngineRef == nil || *j.EngineRef != "g1" {
		t.Fatalf("ref not stored: %+v", j.EngineRef)
	}
	if got := r.get(t, j.ID); got.EngineRef == nil {
		t.Fatal("ref not in db")
	}
}

func TestAddDuplicate(t *testing.T) {
	r := newRig(t)
	r.add(t, AddParams{URIs: []string{"http://Example.com:80/a.iso#x"}})
	_, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/a.iso"}, Dir: r.dir})
	var dup *DuplicateError
	if !errors.As(err, &dup) || dup.Existing == nil || dup.Existing.Name != "a.iso" {
		t.Fatalf("want DuplicateError, got %v", err)
	}
	if _, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/a.iso"}, Dir: r.dir, Force: true}); err != nil {
		t.Fatalf("forced add: %v", err)
	}
}

func TestAddNoSpace(t *testing.T) {
	r := newRig(t)
	r.m.free = func(string) (uint64, error) { return 10, nil }
	_, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/a.iso"}, Dir: r.dir, Total: 100})
	var ns *NoSpaceError
	if !errors.As(err, &ns) || ns.Free != 10 || ns.Need < 100 {
		t.Fatalf("want NoSpaceError, got %v", err)
	}
	if js, _ := r.st.ListJobs(bg, store.Filter{}); len(js) != 0 {
		t.Fatal("refused add left a row")
	}
	r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}}) // size unknown: no check
}

func TestAddSetsAllocationAndDev(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://u:p@example.com/a.iso"}})
	if j.DirDev == nil || *j.DirDev != 7 {
		t.Fatalf("dir_dev %v", j.DirDev)
	}
	req := r.e.adds[0]
	if req.Options["file-allocation"] != "falloc" {
		t.Fatalf("file-allocation %q", req.Options["file-allocation"])
	}
	if req.URIs[0] != "http://example.com/a.iso" || req.Options["http-user"] != "u" || req.Options["http-passwd"] != "p" {
		t.Fatalf("credentials not split: %v %v", req.URIs, req.Options)
	}
	if j.URIs[0] != "http://example.com/a.iso" {
		t.Fatalf("stored uri keeps credentials: %v", j.URIs)
	}
}

func TestAddRejectsUnknownOption(t *testing.T) {
	r := newRig(t)
	_, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/a"}, Dir: r.dir, Options: map[string]string{"on-download-complete": "x"}})
	var inv engine.ErrInvalidOption
	if !errors.As(err, &inv) {
		t.Fatalf("want ErrInvalidOption, got %v", err)
	}
}

func TestPrecheckWhenSizeKnown(t *testing.T) {
	r := newRig(t)
	sub := r.bus.Subscribe(false)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.m.free = func(string) (uint64, error) { return 50, nil }
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.Total = "active", 1000 })
	r.event(ref, engine.KindStart)
	if got := r.get(t, j.ID); got.State != store.Paused {
		t.Fatalf("state %s, want paused", got.State)
	}
	if len(r.e.paused) != 1 {
		t.Fatal("engine not paused")
	}
	for {
		select {
		case ev := <-sub.Events():
			if ev.Type == "banner" && ev.Data["kind"] == "no_space" {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("no no_space banner")
		}
	}
}
