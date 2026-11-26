//go:build e2e && linux

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// TestCrashNeverLoses: kill -9 the daemon or aria2c, in turn, at 23 chosen
// points of progress, three of them above 90%; every job still ends done
// with the right bytes.
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

	// One kill at each 4% of progress up to 80%, then at 91%, 94% and 97%,
	// so kills also land late, near the finalise window. The server holds
	// every file 2% past the next kill point, so each point is reached and
	// none is overshot, however fast or slow the machine.
	var targets []float64
	for p := 4; p <= 80; p += 4 {
		targets = append(targets, float64(p)/100)
	}
	targets = append(targets, 0.91, 0.94, 0.97)
	gate := func(frac float64) {
		for name, b := range files {
			srv.SetGate(name, int64(frac*float64(len(b))))
		}
	}
	start := time.Now()
	for i, target := range targets {
		gate(target + 0.02)
		var frac float64
		waitFor(t, time.Minute, fmt.Sprintf("progress to reach %.0f%%", target*100), func() bool {
			var allDone bool
			frac, allDone = progress()
			if allDone {
				t.Fatalf("all jobs done at a gate of %.0f%%", (target+0.02)*100)
			}
			return frac >= target
		})
		// status auto-starts the daemon after the previous kill.
		pid := e.status().PID
		victim, what := pid, "daemon"
		if i%2 == 1 {
			what = "aria2c"
			waitFor(t, 10*time.Second, "an aria2c child", func() bool { victim = aria2Child(pid); return victim != 0 })
		}
		if err := syscall.Kill(victim, syscall.SIGKILL); err != nil {
			t.Fatalf("kill %s %d: %v", what, victim, err)
		}
		t.Logf("kill %2d: %s %d at %s, progress %.1f%%", i+1, what, victim, time.Since(start).Round(time.Millisecond), frac*100)
	}
	for name := range files {
		srv.SetGate(name, -1)
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
