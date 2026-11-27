//go:build e2e && linux

package e2e

import (
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// TestNoRangeRestartKeepsName: a server that ignores Range cannot continue
// a download from the middle. After a pause or a killed aria2c, the
// download starts over from zero, and the finished file still gets the
// job's own name, with nothing else left in the download folder.
func TestNoRangeRestartKeepsName(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"pause", "kill"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			data := payload(2<<20, 0x61)
			srv := testutil.NewFaultServer(t, map[string][]byte{"nr.bin": data})
			// The no-range mode sends at full speed; the limit keeps the
			// download running long enough to interrupt it.
			e := newEnv(t, fastPoll+"[limits]\nglobal_down = \"512K\"\n")
			id := e.addOne(srv.URL("nr.bin", testutil.ModeNoRange))
			e.waitRunning(id)

			switch how {
			case "pause":
				e.ok("pause", id)
				e.ok("resume", id)
			case "kill":
				pid := e.status().PID
				var child int
				waitFor(t, 10*time.Second, "the aria2c child", func() bool { child = aria2Child(pid); return child != 0 })
				if err := syscall.Kill(child, syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
			}

			j := e.waitState(id, 2*time.Minute, store.Done, store.Error)
			if j.State != store.Done || j.Name != "nr.bin" {
				t.Fatalf("job ended %s as %q (%s)", j.State, j.Name, deref(j.ErrorMsg))
			}
			if n := srv.Served("nr.bin"); n <= int64(len(data)) {
				t.Fatalf("served %d bytes for a %d-byte file; the download did not start over", n, len(data))
			}
			if got := dirNames(t, e.dl); !slices.Equal(got, []string{"nr.bin"}) {
				t.Fatalf("download folder holds %v", got)
			}
			checkFile(t, j, data)
		})
	}
}
