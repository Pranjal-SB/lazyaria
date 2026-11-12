//go:build e2e && linux

package e2e

import (
	"math/rand/v2"
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
	e := newEnv(t, "")
	var links []string
	for name := range files {
		links = append(links, srv.URL(name, testutil.ModeSlow))
	}
	ids := e.add(links...)
	if len(ids) != 3 {
		t.Fatalf("added %d jobs, want 3", len(ids))
	}

	start := time.Now()
	for i := range 20 {
		time.Sleep(time.Duration(100+rand.IntN(700)) * time.Millisecond)
		// status auto-starts the daemon after the previous kill.
		pid := e.status().PID
		victim, what := pid, "daemon"
		if i%2 == 1 {
			what = "aria2c"
			waitFor(t, 10*time.Second, "an aria2c child", func() bool {
				victim = aria2Child(pid)
				return victim != 0
			})
		}
		if err := syscall.Kill(victim, syscall.SIGKILL); err != nil {
			t.Fatalf("kill %s %d: %v", what, victim, err)
		}
		t.Logf("kill %2d: %s %d at %s", i+1, what, victim, time.Since(start).Round(time.Millisecond))
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
