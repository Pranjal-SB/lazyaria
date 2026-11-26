//go:build e2e && linux

package e2e

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// TestIdleZeroProcesses: with the default 30 s idle_exit, once the queue
// empties and no client is attached, nothing of ours runs.
// A user-paused job must not keep the daemon alive.
func TestIdleZeroProcesses(t *testing.T) {
	t.Parallel()
	srv := testutil.NewFaultServer(t, map[string][]byte{
		"idle.bin":   payload(256<<10, 0x21),
		"parked.bin": payload(300<<10, 0x22),
	})
	e := newEnv(t, "")
	parked := e.add("--paused", srv.URL("parked.bin", testutil.ModeOK))
	if len(parked) != 1 {
		t.Fatalf("paused add returned %v", parked)
	}
	e.ok("add", "--wait", srv.URL("idle.bin", testutil.ModeOK))

	if got := e.job(parked[0]).State; got != store.Paused {
		t.Fatalf("parked job is %s, want paused", got)
	}
	pid := e.status().PID
	if aria2Child(pid) == 0 {
		t.Fatal("no aria2c child while the daemon is up")
	}
	hasAria2 := false
	for _, p := range e.procs() {
		if b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", p)); strings.TrimSpace(string(b)) == "aria2c" {
			hasAria2 = true
		}
	}
	if !hasAria2 {
		t.Fatalf("aria2c is not among procs() %v while the daemon runs; the scan is broken", e.procs())
	}
	// The queue is empty and status, the last client, has gone.
	emptied := time.Now()

	// 30 s idle_exit, then the shutdown: aria2c gets SIGTERM and, if it
	// lingers, SIGKILL at the latest 5 s later.
	// An early exit would also pass the zero-process check below.
	time.Sleep(time.Until(emptied.Add(25 * time.Second)))
	if !alive(pid) {
		t.Fatalf("daemon exited %s after the queue emptied, before its 30 s idle_exit", time.Since(emptied).Round(time.Millisecond))
	}
	deadline := emptied.Add(33 * time.Second)
	for len(e.procs()) > 0 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	if ps := e.procs(); len(ps) > 0 {
		t.Fatalf("processes still running %s after the queue emptied: %v", time.Since(emptied).Round(time.Millisecond), ps)
	}
	t.Logf("zero processes %s after the last client left", time.Since(emptied).Round(time.Millisecond))
	if _, err := os.Stat(e.p.Socket); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket left behind after idle exit: %v", err)
	}
}

// TestCallsDuringIdleExit fires CLI calls at a daemon with a 1 s idle_exit,
// spaced so many of them land while it is shutting down: socket gone, lock
// still held. Every call must reach a daemon, old or new.
func TestCallsDuringIdleExit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, "[daemon]\nidle_exit = \"1s\"\n")
	failed := 0
	for i := range 60 {
		if out, errOut, code := e.run("list", "--all"); code != 0 {
			failed++
			t.Logf("call %d exited %d: %s%s", i, code, out, errOut)
		}
		time.Sleep(time.Second + time.Duration(i%10)*60*time.Millisecond)
	}
	if failed > 0 {
		t.Fatalf("%d of 60 calls failed during idle exit", failed)
	}
}
