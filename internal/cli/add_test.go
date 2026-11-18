package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestAddStdin(t *testing.T) {
	f := startFake(t)
	code, out, errOut := run(t, Add, "see https://a/x and magnet:?xt=urn:btih:abc", "-")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(f.adds) != 2 || f.adds[0].URIs[0] != "https://a/x" || f.adds[1].URIs[0] != "magnet:?xt=urn:btih:abc" {
		t.Fatalf("adds = %+v", f.adds)
	}
	if strings.Count(out, "added ") != 2 {
		t.Errorf("stdout %q", out)
	}
}

func TestAddFlagsAndTorrentFile(t *testing.T) {
	f := startFake(t)
	tor := filepath.Join(t.TempDir(), "x.torrent")
	os.WriteFile(tor, []byte("d4:infod4:name1:xee"), 0o600)
	code, _, errOut := run(t, Add, "", "--dir", "rel", "--out", "o.bin", tor, "--paused")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	p := f.adds[0]
	if string(p.Torrent) != "d4:infod4:name1:xee" || p.Out != "o.bin" || !p.StartPaused || !filepath.IsAbs(p.Dir) {
		t.Fatalf("add params %+v", p)
	}
}

func TestAddNotALink(t *testing.T) {
	startFake(t)
	if code, _, errOut := run(t, Add, "", "nonsense"); code != 2 || !strings.Contains(errOut, "nonsense") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if code, _, _ := run(t, Add, ""); code != 2 {
		t.Fatalf("no args: exit %d", code)
	}
}

func TestAddDuplicateExit4(t *testing.T) {
	f := startFake(t)
	f.addErr = &daemon.DuplicateError{Existing: &store.Job{ID: "01JEXISTING0000000000000000", Name: "a.iso", State: store.Done}}
	code, _, errOut := run(t, Add, "", "https://a/a.iso")
	if code != 4 {
		t.Fatalf("exit %d, want 4: %s", code, errOut)
	}
	if !strings.Contains(errOut, "01JEXISTING") || !strings.Contains(errOut, "a.iso") {
		t.Errorf("stderr %q should name the existing job", errOut)
	}
}

func TestAddSkippedIsSuccess(t *testing.T) {
	f := startFake(t)
	f.addErr = &daemon.SkippedError{Existing: &store.Job{ID: "01JEXISTING0000000000000000", Name: "a.iso", State: store.Done}}
	code, out, errOut := run(t, Add, "", "https://a/a.iso")
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, errOut)
	}
	if !strings.Contains(out, "skipped") || !strings.Contains(out, "01JEXISTING") {
		t.Errorf("stdout %q should say skipped and name the job", out)
	}
}

func TestAddWaitFake(t *testing.T) {
	for _, tc := range []struct {
		state store.State
		code  int
	}{{store.Done, 0}, {store.Error, 1}} {
		f := startFake(t)
		go func() {
			for {
				time.Sleep(20 * time.Millisecond)
				f.mu.Lock()
				if len(f.jobs) == 0 {
					f.mu.Unlock()
					continue
				}
				j := *f.jobs[0]
				j.State = tc.state
				f.jobs[0] = &j
				f.mu.Unlock()
				f.bus.Publish(daemon.Event{Type: "job.updated", Job: &j})
				return
			}
		}()
		code, _, errOut := run(t, Add, "", "--wait", "https://a/b")
		if code != tc.code {
			t.Errorf("%s: exit %d, want %d: %s", tc.state, code, tc.code, errOut)
		}
	}
}

// A job the daemon pauses (no space, disk full) will not finish by itself,
// so --wait ends with the reason and exit 1. A daemon older than the jobs
// list in the disk_full banner is matched by folder.
func TestAddWaitEndsOnSystemPause(t *testing.T) {
	for _, tc := range []struct {
		kind      string
		oldDaemon bool
	}{{"no_space", false}, {"disk_full", false}, {"disk_full", true}} {
		kind := tc.kind
		f := startFake(t)
		go func() {
			for {
				time.Sleep(20 * time.Millisecond)
				f.mu.Lock()
				if len(f.jobs) == 0 {
					f.mu.Unlock()
					continue
				}
				j := *f.jobs[0]
				j.State = store.Paused
				f.jobs[0] = &j
				f.mu.Unlock()
				f.bus.Publish(daemon.Event{Type: "job.updated", Job: &j})
				data := map[string]any{"kind": kind, "job": j.ID, "dir": j.Dir, "jobs": []string{j.ID}}
				if tc.oldDaemon {
					delete(data, "jobs")
				}
				f.bus.Publish(daemon.Event{Type: "banner", Data: data})
				return
			}
		}()
		code, _, errOut := run(t, Add, "", "--wait", "https://a/b")
		if code != exitRuntime || !strings.Contains(errOut, "paused") || !strings.Contains(errOut, "space") {
			t.Errorf("%+v: exit %d, stderr %q; want 1 with the reason", tc, code, errOut)
		}
	}
}

// A job the user paused is not ended by a disk_full banner for its folder
// that does not name it.
func TestAddWaitKeepsUserPauseOnDiskFull(t *testing.T) {
	f := startFake(t)
	go func() {
		for {
			time.Sleep(20 * time.Millisecond)
			f.mu.Lock()
			if len(f.jobs) == 0 {
				f.mu.Unlock()
				continue
			}
			j := *f.jobs[0]
			j.State = store.Paused
			f.jobs[0] = &j
			f.mu.Unlock()
			f.bus.Publish(daemon.Event{Type: "job.updated", Job: &j})
			f.bus.Publish(daemon.Event{Type: "banner", Data: map[string]any{"kind": "disk_full", "dir": j.Dir, "jobs": []string{"01OTHER"}}})
			time.Sleep(100 * time.Millisecond)
			done := j // the published j may still be being sent
			done.State = store.Done
			f.bus.Publish(daemon.Event{Type: "job.updated", Job: &done})
			return
		}
	}()
	code, _, errOut := run(t, Add, "", "--wait", "https://a/b")
	if code != 0 {
		t.Errorf("exit %d, stderr %q; want 0: the user's pause is not the disk's", code, errOut)
	}
}

func TestDaemonUnreachableExit3(t *testing.T) {
	isolate(t)
	code, _, errOut := run(t, List, "")
	if code != 3 {
		t.Fatalf("exit %d, want 3: %s", code, errOut)
	}
	if !strings.Contains(errOut, "lazyaria doctor") {
		t.Errorf("stderr %q should point to lazyaria doctor", errOut)
	}
}

func TestExitCodes(t *testing.T) {
	if !slices.Equal([]int{exitCode(nil), exitCode(&daemon.DuplicateError{Path: "/x"}), exitCode(&daemon.SkippedError{Path: "/x"}), exitCode(&daemon.ExistsError{Path: "/x"}), exitCode(engine.ErrInvalidOption{Name: "out", Reason: "r"})}, []int{0, 4, 0, 4, 2}) {
		t.Fatal("exit codes")
	}
}

func TestAddWaitJobRemovedBeforeSnapshot(t *testing.T) {
	f := startFake(t)
	f.dropAdded = true
	code, _, errOut := run(t, Add, "", "--wait", "https://a/b")
	if code != 1 || !strings.Contains(errOut, "removed before it finished") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestAddUsageErrors(t *testing.T) {
	startFake(t)
	for _, args := range [][]string{
		{"--paused", "--wait", "https://a/b"},
		{"--out", "x.bin", "https://a/b", "https://a/c"},
	} {
		if code, _, errOut := run(t, Add, "", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2: %s", args, code, errOut)
		}
	}
}

// The duplicate error tells the user to add again with --force.
func TestAddForce(t *testing.T) {
	f := startFake(t)
	if code, _, errOut := run(t, Add, "", "--force", "https://a/a.iso"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !f.adds[0].Force {
		t.Fatalf("add params %+v, want Force", f.adds[0])
	}
	run(t, Add, "", "https://a/b.iso")
	if f.adds[1].Force {
		t.Fatal("Force set without --force")
	}
}

// --wait asks the daemon only for its own jobs, not the whole history.
func TestAddWaitAsksOnlyForItsJobs(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	f.dropAdded = true // the job is gone at once, so --wait ends after one snapshot
	run(t, Add, "", "--wait", "https://a/b")
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.snapIDs) != 1 || f.snapIDs[0] == f.jobs[0].ID || f.snapIDs[0] == f.jobs[1].ID {
		t.Fatalf("snapshot asked for %v, want only the added job", f.snapIDs)
	}
}
