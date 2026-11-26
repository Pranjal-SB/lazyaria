//go:build e2e && linux

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// fastPoll makes the daemon read aria2 every second while no client
// watches, so the CLI sees progress, and the marker appears, without delay.
const fastPoll = "[advanced]\nsentinel_interval = \"1s\"\n"

// TestStagedDownloadAppearsOnlyWhenDone: while a download runs, the
// download folder holds only the hidden staging folder and the name.laria
// marker; once it finishes, only the file, with the right bytes.
func TestStagedDownloadAppearsOnlyWhenDone(t *testing.T) {
	t.Parallel()
	data := payload(512<<10, 0x51)
	srv := testutil.NewFaultServer(t, map[string][]byte{"st.bin": data})
	e := newEnv(t, fastPoll)
	id := e.addOne(srv.URL("st.bin", testutil.ModeSlow))
	e.waitRunning(id)

	if got := dirNames(t, e.dl); !slices.Equal(got, []string{".laria", "st.bin.laria"}) {
		t.Fatalf("download folder holds %v while downloading", got)
	}
	if got, err := os.Readlink(filepath.Join(e.dl, "st.bin.laria")); err != nil || got != filepath.Join(".laria", id, "st.bin") {
		t.Fatalf("marker points to %q (%v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(e.dl, "st.bin.laria")); err != nil {
		t.Fatalf("marker does not reach the partial file: %v", err)
	}

	j := e.waitState(id, time.Minute, store.Done, store.Error)
	if j.State != store.Done || j.Name != "st.bin" {
		t.Fatalf("job ended %s as %q (%s)", j.State, j.Name, deref(j.ErrorMsg))
	}
	if got := dirNames(t, e.dl); !slices.Equal(got, []string{"st.bin"}) {
		t.Fatalf("download folder holds %v when done", got)
	}
	checkFile(t, j, data)
}

// TestNameTakenWhileDownloading: a file the user puts at the download's
// name meanwhile is kept, and ours is published beside it.
func TestNameTakenWhileDownloading(t *testing.T) {
	t.Parallel()
	data := payload(512<<10, 0x52)
	srv := testutil.NewFaultServer(t, map[string][]byte{"nt.bin": data})
	e := newEnv(t, fastPoll)
	id := e.addOne(srv.URL("nt.bin", testutil.ModeSlow))
	e.waitRunning(id)
	user := filepath.Join(e.dl, "nt.bin")
	mine := []byte("the user's own file\n")
	if err := os.WriteFile(user, mine, 0o644); err != nil {
		t.Fatal(err)
	}

	j := e.waitState(id, time.Minute, store.Done, store.Error)
	if j.State != store.Done || j.Name != "nt.1.bin" {
		t.Fatalf("job ended %s as %q (%s)", j.State, j.Name, deref(j.ErrorMsg))
	}
	if b, _ := os.ReadFile(user); !bytes.Equal(b, mine) {
		t.Fatal("the user's file changed")
	}
	if got := dirNames(t, e.dl); !slices.Equal(got, []string{"nt.1.bin", "nt.bin"}) {
		t.Fatalf("download folder holds %v", got)
	}
	checkFile(t, j, data)
}

// TestCrashDuringPublishRecovers kills the daemon at two points of the move
// into the download folder; the next start finishes it with one copy.
func TestCrashDuringPublishRecovers(t *testing.T) {
	t.Parallel()
	for _, step := range []string{"intent", "moved"} {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			data := payload(256<<10, 0x53)
			srv := testutil.NewFaultServer(t, map[string][]byte{"cr.bin": data})
			e := newEnv(t, "")
			plain := e.vars
			e.vars = append(slices.Clone(plain), "LAZYARIA_E2E_CRASH_AT="+step)
			pid := e.status().PID // this daemon carries the crash hook
			id := e.addOne(srv.URL("cr.bin", testutil.ModeSlow))
			e.vars = plain // the next daemon does not
			waitFor(t, time.Minute, "the daemon to die at "+step, func() bool { return !alive(pid) })

			staged, final := filepath.Join(e.dl, ".laria", id, "cr.bin"), filepath.Join(e.dl, "cr.bin")
			if lexists(filepath.Join(e.dl, "cr.bin.laria")) {
				t.Error("marker left after the move was recorded")
			}
			switch step {
			case "intent":
				if !lexists(staged) || lexists(final) {
					t.Fatalf("after the crash: staged %v, published %v", lexists(staged), lexists(final))
				}
			case "moved":
				if lexists(staged) || !lexists(final) {
					t.Fatalf("after the crash: staged %v, published %v", lexists(staged), lexists(final))
				}
			}

			j := e.waitState(id, time.Minute, store.Done, store.Error) // starts a daemon
			if j.State != store.Done || j.Name != "cr.bin" {
				t.Fatalf("job ended %s as %q (%s)", j.State, j.Name, deref(j.ErrorMsg))
			}
			if got := dirNames(t, e.dl); !slices.Equal(got, []string{"cr.bin"}) {
				t.Fatalf("download folder holds %v", got)
			}
			checkFile(t, j, data)
		})
	}
}

// TestRemoveStagedWithFilesAndUndo: remove --delete-files takes the
// unfinished download out of the download folder, and undo brings it back
// to finish.
func TestRemoveStagedWithFilesAndUndo(t *testing.T) {
	t.Parallel()
	data := payload(512<<10, 0x54)
	srv := testutil.NewFaultServer(t, map[string][]byte{"rm.bin": data})
	e := newEnv(t, fastPoll)
	id := e.addOne(srv.URL("rm.bin", testutil.ModeSlow))
	e.waitRunning(id)

	out := e.ok("remove", "--delete-files", id)
	_, tok, ok := strings.Cut(out, "lazyaria undo ")
	if !ok {
		t.Fatalf("remove printed no undo command:\n%s", out)
	}
	tok = strings.TrimSpace(tok)
	for _, j := range e.jobs() {
		if j.ID == id {
			t.Fatalf("job still listed as %s", j.State)
		}
	}
	for _, p := range []string{filepath.Join(".laria", id), "rm.bin.laria", "rm.bin"} {
		if lexists(filepath.Join(e.dl, p)) {
			t.Fatalf("%s left in the download folder after remove", p)
		}
	}
	// The trash shows the unfinished download by its name, not its id.
	if trashed := filepath.Join(e.root, "data", "Trash", "files", "rm.bin (unfinished)"); !lexists(filepath.Join(trashed, "rm.bin")) {
		t.Fatalf("no partial file in %s", trashed)
	}

	e.ok("undo", tok)
	if j := e.job(id); j.State != store.Paused {
		t.Fatalf("after undo the job is %s, want paused", j.State)
	}
	if !lexists(filepath.Join(e.dl, ".laria", id, "rm.bin")) {
		t.Fatal("undo did not bring back the partial file")
	}
	e.ok("resume", id)
	j := e.waitState(id, time.Minute, store.Done, store.Error)
	if j.State != store.Done || j.Name != "rm.bin" {
		t.Fatalf("job ended %s as %q (%s)", j.State, j.Name, deref(j.ErrorMsg))
	}
	if got := dirNames(t, e.dl); !slices.Equal(got, []string{"rm.bin"}) {
		t.Fatalf("download folder holds %v", got)
	}
	checkFile(t, j, data)
}

// addOne adds one link and returns its job id.
func (e *env) addOne(link string) string {
	e.t.Helper()
	ids := e.add(link)
	if len(ids) != 1 {
		e.t.Fatalf("add returned %v", ids)
	}
	return ids[0]
}

// waitRunning waits until the job is active and has written some bytes.
func (e *env) waitRunning(id string) {
	e.t.Helper()
	waitFor(e.t, 30*time.Second, "job "+id+" to download", func() bool {
		j := e.job(id)
		return j.State == store.Active && j.DoneBytes != nil && *j.DoneBytes > 0
	})
}

// dirNames is the sorted entry names in dir.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
