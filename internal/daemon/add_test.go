package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	if len(js) != 1 || js[0].State != store.Queued || js[0].EngineRef == nil || !gidRe.MatchString(*js[0].EngineRef) {
		t.Fatalf("before engine add, want a queued row with a gid: %+v", js)
	}
	gid := *js[0].EngineRef
	close(r.e.addBlock)
	j := <-done
	if j.EngineRef == nil || *j.EngineRef != gid || r.e.adds[0].GID != gid {
		t.Fatalf("ref %v, want the pre-assigned gid %s", j.EngineRef, gid)
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

// A magnet matches by info-hash, in hex or base32; a .torrent file matches
// by the hash of its info dictionary, also against a magnet.
func TestAddDuplicateTorrents(t *testing.T) {
	r := newRig(t)
	isDup := func(p AddParams) bool {
		t.Helper()
		p.Dir = r.dir
		_, err := r.m.Add(bg, p)
		var dup *DuplicateError
		if err != nil && !errors.As(err, &dup) {
			t.Fatal(err)
		}
		return dup != nil
	}
	mag := func(xt string) AddParams { return AddParams{URIs: []string{"magnet:?xt=urn:btih:" + xt + "&dn=x"}} }
	r.add(t, mag("0123456789ABCDEF0123456789ABCDEF01234567"))
	if !isDup(mag("0123456789abcdef0123456789abcdef01234567")) || !isDup(mag("AERUKZ4JVPG66AJDIVTYTK6N54ASGRLH")) {
		t.Fatal("the same magnet was not a duplicate")
	}
	tor := AddParams{Torrent: []byte("d8:announce1:a4:infod6:lengthi1e4:name1:xee")}
	if isDup(tor) {
		t.Fatal("first add of the .torrent was a duplicate")
	}
	if !isDup(tor) || !isDup(mag("607361303e836d09e97091ea3a6fc0ae34cb1ebd")) {
		t.Fatal("the same .torrent, or its magnet, was not a duplicate")
	}
	tor.Force = true
	if isDup(tor) {
		t.Fatal("--force did not add the .torrent again")
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

var gidRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestAddSurvivesCallerCancel(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(bg)
	r.e.onAdd = func(ref engine.EngineRef) {
		cancel() // the client hung up right after the engine took the add
		r.e.setState(ref, "active")
	}
	j, err := r.m.Add(ctx, AddParams{URIs: []string{"http://example.com/a.iso"}, Dir: r.dir})
	if err != nil {
		t.Fatal(err)
	}
	got := r.get(t, j.ID)
	if got.EngineRef == nil || *got.EngineRef != r.e.adds[0].GID || got.State != store.Active {
		t.Fatalf("after cancel: ref %v state %s", got.EngineRef, got.State)
	}
}

func TestAddDuplicateModes(t *testing.T) {
	for _, mode := range []string{"ask", "allow", "skip"} {
		t.Run(mode, func(t *testing.T) {
			r := newRig(t)
			r.m.s.Load().Reliability.Duplicates = mode
			first := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
			j, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/a.iso"}, Dir: r.dir})
			var dup *DuplicateError
			switch mode {
			case "ask":
				if !errors.As(err, &dup) || dup.Existing == nil {
					t.Fatalf("url: want DuplicateError, got %v", err)
				}
			case "allow":
				if err != nil || j.ID == first.ID {
					t.Fatalf("url: %v", err)
				}
			case "skip":
				var skip *SkippedError
				if !errors.As(err, &skip) || skip.Existing == nil || skip.Existing.ID != first.ID || j.ID != first.ID || skip.Error() == "" {
					t.Fatalf("url: want SkippedError with the existing job, got %v %v", j, err)
				}
			}

			os.WriteFile(filepath.Join(r.dir, "done.bin"), []byte("x"), 0o600)
			_, err = r.m.Add(bg, AddParams{URIs: []string{"http://other.example.com/done.bin"}, Dir: r.dir})
			var skip *SkippedError
			switch mode {
			case "ask":
				if !errors.As(err, &dup) || dup.Existing != nil || dup.Path != filepath.Join(r.dir, "done.bin") || dup.Error() == "" {
					t.Fatalf("path: want DuplicateError with Path, got %v", err)
				}
			case "allow":
				if err != nil {
					t.Fatalf("path: %v", err)
				}
			case "skip":
				if !errors.As(err, &skip) {
					t.Fatalf("path: want SkippedError, got %v", err)
				}
			}
		})
	}
}

func TestAddPartialTargetIsNotDuplicate(t *testing.T) {
	r := newRig(t)
	os.WriteFile(filepath.Join(r.dir, "part.bin"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(r.dir, "part.bin.aria2"), []byte("x"), 0o600)
	r.add(t, AddParams{URIs: []string{"http://example.com/part.bin"}})
}

func TestStartPausedIntentSurvivesLostAdd(t *testing.T) {
	r := newRig(t)
	down := &downEngine{fakeEngine: r.e}
	r.m.e = down
	j, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/a.iso"}, Dir: r.dir, StartPaused: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.get(t, j.ID); got.State != store.Paused || got.EngineRef == nil {
		t.Fatalf("intent row %s ref %v, want paused with a gid", got.State, got.EngineRef)
	}
	r.m.e = r.e // the engine is back
	if err := r.m.Reconcile(bg); err != nil {
		t.Fatal(err)
	}
	if len(r.e.adds) != 1 || !r.e.adds[0].Paused {
		t.Fatalf("re-add %+v, want one paused add", r.e.adds)
	}
	if got := r.get(t, j.ID); got.State != store.Paused {
		t.Fatalf("state %s, want paused", got.State)
	}
}

func TestAddOnExists(t *testing.T) {
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	setup := func(t *testing.T, mode string) *rig {
		r := newRig(t)
		r.m.s.Load().Reliability.Duplicates = "allow"
		r.m.s.Load().Downloads.OnExists = mode
		for _, n := range []string{"ex.bin", "ex.1.bin", "my.iso"} {
			os.WriteFile(filepath.Join(r.dir, n), []byte("mine"), 0o600)
		}
		return r
	}
	t.Run("rename", func(t *testing.T) {
		r := setup(t, "rename")
		j := r.add(t, AddParams{URIs: []string{"http://example.com/ex.bin"}})
		req := r.e.adds[0]
		if j.Name != "ex.2.bin" || deref(j.Out) != "ex.2.bin" || req.Out != "ex.2.bin" || !req.NoContinue {
			t.Fatalf("name %q out %q; request out %q continue=false %v", j.Name, deref(j.Out), req.Out, req.NoContinue)
		}
		if got := r.get(t, j.ID); deref(got.Out) != "ex.2.bin" {
			t.Fatalf("db out %q", deref(got.Out))
		}
		// An explicit --out with Force, under the default duplicates mode.
		r.m.s.Load().Reliability.Duplicates = "ask"
		j, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/x"}, Out: "my.iso", Dir: r.dir, Force: true})
		if err != nil || j.Name != "my.1.iso" || r.e.adds[1].Out != "my.1.iso" {
			t.Fatalf("forced --out: %v name %q", err, j.Name)
		}
	})
	t.Run("overwrite", func(t *testing.T) {
		// An HTTP link to a .torrent: aria2 would hand allow-overwrite on to
		// the torrent it starts, so overwrite trashes instead.
		t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
		r := setup(t, "overwrite")
		orig := filepath.Join(r.dir, "ex.torrent")
		os.WriteFile(orig, []byte("mine"), 0o600)
		j := r.add(t, AddParams{URIs: []string{"http://example.com/ex.torrent"}})
		req := r.e.adds[0]
		if _, ok := req.Options["allow-overwrite"]; ok || j.Name != "ex.torrent" || j.Out != nil || !req.NoContinue {
			t.Fatalf("name %q out %q options %v continue=false %v", j.Name, deref(j.Out), req.Options, req.NoContinue)
		}
		if exists(orig) {
			t.Fatal("the existing file is still in place")
		}
		evs, _ := r.st.Events(bg, j.ID)
		var trashed string
		for _, e := range evs {
			if e.Kind == "trashed-existing" {
				trashed = e.Detail
			}
		}
		if b, err := os.ReadFile(trashed); err != nil || string(b) != "mine" {
			t.Fatalf("trashed copy %q: %v", trashed, err)
		}
	})
	t.Run("ask", func(t *testing.T) {
		r := setup(t, "ask")
		_, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/ex.bin"}, Dir: r.dir})
		var ex *ExistsError
		if !errors.As(err, &ex) || ex.Path != filepath.Join(r.dir, "ex.bin") || !strings.Contains(ex.Error(), "downloads.on_exists") {
			t.Fatalf("want ExistsError, got %v", err)
		}
		if js, _ := r.st.ListJobs(bg, store.Filter{}); len(js) != 0 || len(r.e.adds) != 0 {
			t.Fatalf("ask added anyway: %d jobs, %d engine adds", len(js), len(r.e.adds))
		}
	})
	t.Run("no-out", func(t *testing.T) {
		// The server names the file: aria2 renames it, guarded by continue=false.
		r := setup(t, "rename")
		j := r.add(t, AddParams{URIs: []string{"http://example.com/"}})
		if req := r.e.adds[0]; j.Out != nil || req.Out != "" || !req.NoContinue {
			t.Fatalf("out %q request %+v", deref(j.Out), req)
		}
	})
}

// options.out is the same request as Out: it is sanitised and checked
// against existing files, and never reaches aria2 as written.
func TestAddOptionsOutIsChecked(t *testing.T) {
	r := newRig(t)
	os.WriteFile(filepath.Join(r.dir, "taken.bin"), []byte("mine"), 0o600)
	for _, c := range []struct{ out, want string }{{"../x", "x"}, {"taken.bin", "taken.1.bin"}} {
		j := r.add(t, AddParams{URIs: []string{"http://example.com/" + c.out}, Options: map[string]string{"out": c.out}, Force: true})
		req := r.e.adds[len(r.e.adds)-1]
		if j.Out == nil || *j.Out != c.want || req.Out != c.want {
			t.Fatalf("out %q: job out %v, request out %q; want %q", c.out, j.Out, req.Out, c.want)
		}
		if _, ok := req.Options["out"]; ok {
			t.Fatalf("out %q: options.out still sent: %v", c.out, req.Options)
		}
	}
}

func TestAddBTRejectsOut(t *testing.T) {
	r := newRig(t)
	for _, p := range []AddParams{
		{URIs: []string{"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}, Out: "x"},
		{Torrent: []byte("d4:infod4:name1:xee"), Options: map[string]string{"out": "x"}},
	} {
		p.Dir = r.dir
		_, err := r.m.Add(bg, p)
		if inv, ok := errors.AsType[engine.ErrInvalidOption](err); !ok || inv.Name != "out" || !strings.Contains(err.Error(), "drop --out") {
			t.Fatalf("want ErrInvalidOption out, got %v", err)
		}
	}
	if len(r.e.adds) != 0 {
		t.Fatal("added anyway")
	}
}

// A name held by a live job is taken even when its .aria2 sits there: that
// control file is the other job's progress.
func TestAddHeldPartialIsRenamed(t *testing.T) {
	r := newRig(t)
	part := filepath.Join(r.dir, "ex.bin")
	os.WriteFile(part, []byte("half"), 0o600)
	os.WriteFile(part+".aria2", []byte("ctl"), 0o600)
	a := r.add(t, AddParams{URIs: []string{"http://a.example.com/ex.bin"}})
	if err := r.m.Pause(bg, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	b, err := r.m.Add(bg, AddParams{URIs: []string{"http://b.example.com/ex.bin"}, Dir: r.dir + "/"})
	if err != nil || b.Name != "ex.1.bin" || r.e.adds[1].Out != "ex.1.bin" {
		t.Fatalf("job B: %v name %q", err, b.Name)
	}
	if got, _ := os.ReadFile(part); string(got) != "half" {
		t.Fatal("job A's file changed")
	}
}

// Live jobs hold their names before their files exist on disk.
func TestAddRenameSkipsHeldNames(t *testing.T) {
	r := newRig(t)
	os.WriteFile(filepath.Join(r.dir, "ex.bin"), []byte("mine"), 0o600)
	var names []string
	for range 2 {
		j, err := r.m.Add(bg, AddParams{URIs: []string{"http://example.com/ex.bin"}, Dir: r.dir, Force: true})
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, j.Name)
	}
	if names[0] != "ex.1.bin" || names[1] != "ex.2.bin" {
		t.Fatalf("names %v, want ex.1.bin then ex.2.bin", names)
	}
}

func TestFreeNameFitsLimit(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("é", 122) + "x.bin" // 249 bytes, the cap
	os.WriteFile(filepath.Join(dir, long), nil, 0o600)
	got, err := freeName(filepath.Join(dir, long), nil)
	// name.aria2 must still fit in 255 bytes.
	if err != nil || len(got) > 249 || len(got+".aria2") > 255 || !strings.HasSuffix(got, ".1.bin") || !utf8.ValidString(got) {
		t.Fatalf("%q (%d bytes) %v", got, len(got), err)
	}
	if got, _ := freeName(filepath.Join(dir, "README"), nil); got != "README.1" {
		t.Fatalf("no extension: %q", got)
	}
}

// A link url.Parse rejects keeps its userinfo, so it must be refused
// before a row (shown by list, snapshots and search) is written, and the
// error must not echo it.
func TestAddRejectsUnparseableLinkBeforeStoring(t *testing.T) {
	r := newRig(t)
	_, err := r.m.Add(bg, AddParams{URIs: []string{"https://alice:p%zzword@host/f"}, Dir: r.dir})
	if err == nil || strings.Contains(err.Error(), "p%zzword") || strings.Contains(err.Error(), "alice") {
		t.Fatalf("err = %v, want a refusal that does not echo the link", err)
	}
	js, err := r.st.ListJobs(bg, store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 0 {
		t.Fatalf("a row was stored: %+v", js[0].URIs)
	}
}
