//go:build e2e && linux

package e2e

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// TestCrashNeverLoses is quality bar Q1: kill -9 the daemon or aria2c at
// random points 20 times; every job still ends done with the right bytes.
func TestCrashNeverLoses(t *testing.T) {
	files := map[string][]byte{}
	for i, name := range []string{"c1.bin", "c2.bin", "c3.bin"} {
		files[name] = payload(4<<20+i*4096, byte(0x11*(i+1)))
	}
	srv := testutil.NewFaultServer(t, files)
	// list --json refreshes on the sentinel poll when nobody observes; at the
	// 15 s default one refresh can jump past several kill thresholds.
	e := newEnv(t, "[advanced]\nsentinel_interval = \"1s\"\n")
	var links []string
	for name := range files {
		links = append(links, srv.URL(name, testutil.ModeSlow))
	}
	ids := e.add(links...)
	if len(ids) != 3 {
		t.Fatalf("added %d jobs, want 3", len(ids))
	}

	var total int64
	for _, b := range files {
		total += int64(len(b))
	}
	// progress is the share of all bytes written, from lazyaria list --json.
	progress := func() (frac float64, allDone bool) {
		var done int64
		allDone = true
		for _, j := range e.jobs() {
			switch {
			case j.State == store.Done:
				done += int64(len(files[j.Name]))
			case j.DoneBytes != nil:
				done += *j.DoneBytes
				allDone = false
			default:
				allDone = false
			}
		}
		return float64(done) / float64(total), allDone
	}

	// Kill until every job is done: one kill per 5% of progress up to 90%,
	// then one as progress first crosses each of 91%, 94% and 97%, so kills
	// also land in the finalise window (.aria2 removal, Done). A threshold
	// the downloads finish before is skipped.
	start := time.Now()
	const minKills = 20
	lateAt := []float64{0.91, 0.94, 0.97}
	kills, late := 0, 0
	for time.Since(start) < 150*time.Second {
		frac, allDone := progress()
		if allDone {
			break
		}
		if frac > 0.9 {
			if len(lateAt) == 0 {
				break // the late kills are done; let the jobs finish
			}
			// Poll fast and kill the moment the next threshold is crossed.
			for !allDone && len(lateAt) > 0 && frac < lateAt[0] && time.Since(start) < 150*time.Second {
				time.Sleep(100 * time.Millisecond)
				frac, allDone = progress()
			}
			if allDone || frac < lateAt[0] {
				break // done, or the deadline passed before the threshold
			}
			for len(lateAt) > 0 && frac >= lateAt[0] {
				lateAt = lateAt[1:]
			}
		}
		// status auto-starts the daemon after the previous kill.
		pid := e.status().PID
		victim, what := pid, "daemon"
		if kills%2 == 1 {
			what = "aria2c"
			waitFor(t, 10*time.Second, "an aria2c child or completion", func() bool {
				victim = aria2Child(pid)
				_, allDone = progress()
				return victim != 0 || allDone
			})
			if victim == 0 {
				break
			}
		}
		if err := syscall.Kill(victim, syscall.SIGKILL); err != nil {
			t.Fatalf("kill %s %d: %v", what, victim, err)
		}
		kills++
		if frac > 0.9 {
			late++
		}
		t.Logf("kill %2d: %s %d at %s, progress %.1f%%", kills, what, victim, time.Since(start).Round(time.Millisecond), frac*100)
		if frac > 0.9 {
			continue // the threshold loop above paces the late kills
		}
		// Wait for the next 5% (or 4 s, if a restart makes no headway).
		target, until := frac+0.05, time.Now().Add(4*time.Second)
		for time.Now().Before(until) {
			if f, done := progress(); f >= target || done {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	t.Logf("%d kills, %d up to 90%% progress, %d after", kills, kills-late, late)
	if kills < minKills {
		t.Errorf("only %d kills, want at least %d", kills, minKills)
	}
	if late < 2 {
		t.Errorf("only %d kills above 90%% progress, want at least 2", late)
	}

	for _, id := range ids {
		j := e.waitState(id, 2*time.Minute, store.Done, store.Error)
		if j.State != store.Done {
			t.Fatalf("job %s (%s) ended %s: %v", id, j.Name, j.State, deref(j.ErrorMsg))
		}
		want, ok := files[j.Name]
		if !ok {
			t.Fatalf("job %s finished as %q, not one of the served names", id, j.Name)
		}
		path := filepath.Join(j.Dir, j.Name)
		if got := testutil.SHA256File(t, path); got != sha(want) {
			t.Errorf("%s: sha256 %s, want %s", path, got, sha(want))
		}
		if _, err := os.Stat(path + ".aria2"); err == nil {
			t.Errorf("%s.aria2 control file left behind", path)
		}
	}
	t.Logf("all 3 done %s after the first kill", time.Since(start).Round(time.Millisecond))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
