//go:build e2e && linux

package e2e

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// fastRetry keeps aria2's own retries and lazyaria's backoff short.
const fastRetry = "[retry]\nmax_tries = 2\nretry_wait = \"1s\"\nbackoff_base = \"1s\"\n"

// TestFailurePaths: every failure a download can meet ends in the
// documented state, and the recoverable ones recover.
func TestFailurePaths(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		mode testutil.Mode
		kind string
	}{
		{testutil.Mode404, "not-found"},
		{testutil.Mode410, "link-expired"},
		{testutil.Mode503, "server-busy"},
	} {
		t.Run(string(c.mode), func(t *testing.T) {
			t.Parallel()
			srv := testutil.NewFaultServer(t, map[string][]byte{"f.bin": payload(64<<10, 0x41)})
			e := newEnv(t, fastRetry)
			j := e.failed(srv.URL("f.bin", c.mode))
			code := -1
			if j.ErrorCode != nil {
				code = *j.ErrorCode
			}
			t.Logf("error %s, aria2 code %d: %s", deref(j.ErrorKind), code, deref(j.ErrorMsg))
			if got := deref(j.ErrorKind); got != c.kind {
				t.Errorf("error kind %q, want %q (message %q)", got, c.kind, deref(j.ErrorMsg))
			}
		})
	}
	t.Run("403-refresh", testExpiredRefresh)
	t.Run("429-retry", testRateLimited)
	t.Run("exists-rename", testExistsRename)
	t.Run("disk-full", testDiskFull)
	t.Run("crash-loop", testCrashLoop)
}

// failed adds link and waits for the job to fail.
func (e *env) failed(link string) *store.Job {
	e.t.Helper()
	ids := e.add(link)
	if len(ids) != 1 {
		e.t.Fatalf("add returned %v", ids)
	}
	j := e.waitState(ids[0], time.Minute, store.Error, store.Done)
	if j.State != store.Error {
		e.t.Fatalf("job ended %s, want error", j.State)
	}
	return j
}

// checkFile asserts the job's file holds want.
func checkFile(t *testing.T, j *store.Job, want []byte) {
	t.Helper()
	path := filepath.Join(j.Dir, j.Name)
	if got := testutil.SHA256File(t, path); got != sha(want) {
		t.Errorf("%s: sha256 %s, want %s", path, got, sha(want))
	}
}

func testExpiredRefresh(t *testing.T) {
	t.Parallel()
	data := payload(512<<10, 0x42)
	srv := testutil.NewFaultServer(t, map[string][]byte{"exp.bin": data})
	e := newEnv(t, fastRetry)
	j := e.failed(srv.URL("exp.bin", testutil.Mode403))
	if got := deref(j.ErrorKind); got != "link-expired" {
		t.Fatalf("error kind %q, want link-expired", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := e.dial().Refresh(ctx, j.ID, srv.URL("exp.bin", testutil.ModeOK)); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	j = e.waitState(j.ID, 30*time.Second, store.Done, store.Error)
	if j.State != store.Done {
		t.Fatalf("after refresh the job is %s (%s)", j.State, deref(j.ErrorMsg))
	}
	checkFile(t, j, data)
}

func testRateLimited(t *testing.T) {
	t.Parallel()
	data := payload(256<<10, 0x43)
	srv := testutil.NewFaultServer(t, map[string][]byte{"rl.bin": data})
	e := newEnv(t, fastRetry)
	ids := e.add(srv.URL("rl.bin", testutil.Mode429))
	if len(ids) != 1 {
		t.Fatalf("add returned %v", ids)
	}
	e.waitState(ids[0], 30*time.Second, store.RetryWait)
	srv.SetMode("rl.bin", testutil.ModeOK)
	j := e.waitState(ids[0], time.Minute, store.Done, store.Error)
	if j.State != store.Done {
		t.Fatalf("job ended %s (%s), want done after the server recovered", j.State, deref(j.ErrorMsg))
	}
	// retry_count is cleared on done, so count the requests instead.
	if n := srv.Hits("rl.bin"); n < 2 {
		t.Errorf("server saw %d requests, want the 429 and at least one retry", n)
	}
	checkFile(t, j, data)
}

// testExistsRename: by default a link whose file already exists is refused
// with exit 4; when allowed, aria2 renames the new file and the job follows.
func testExistsRename(t *testing.T) {
	t.Parallel()
	data := payload(128<<10, 0x44)
	srv := testutil.NewFaultServer(t, map[string][]byte{"ex.bin": data})
	e := newEnv(t, "")
	old := []byte("an unrelated file the user already had\n")
	orig := filepath.Join(e.dl, "ex.bin")
	if err := os.WriteFile(orig, old, 0o644); err != nil {
		t.Fatal(err)
	}
	link := srv.URL("ex.bin", testutil.ModeOK)
	if out, errOut, code := e.run("add", link); code != 4 {
		t.Fatalf("add over an existing file exited %d, want 4\n%s%s", code, out, errOut)
	}
	e.ok("config", "set", "reliability.duplicates", "allow")
	ids := e.add(link)
	if len(ids) != 1 {
		t.Fatalf("add returned %v", ids)
	}
	j := e.waitState(ids[0], 30*time.Second, store.Done, store.Error)
	if j.State != store.Done {
		t.Fatalf("job ended %s (%s)", j.State, deref(j.ErrorMsg))
	}
	if j.Name == "ex.bin" || j.Out == nil || *j.Out != j.Name {
		t.Errorf("job name %q out %q; want the renamed file tracked", j.Name, deref(j.Out))
	}
	checkFile(t, j, data)
	if b, _ := os.ReadFile(orig); !bytes.Equal(b, old) {
		t.Errorf("the existing file was changed (now %d bytes; requests sent Range %q)", len(b), srv.Ranges("ex.bin"))
	}
}
