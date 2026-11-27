package cli

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := "../../testdata/cli/" + name
	if *update {
		os.MkdirAll("../../testdata/cli", 0o755)
		os.WriteFile(path, []byte(got), 0o644)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (run with -update to accept):\n%s", path, got)
	}
}

func ptr[T any](v T) *T { return &v }

func sampleJobs() []*store.Job {
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	return []*store.Job{
		{ID: "01JC0000000000000000000001", Num: 1, Kind: "http", Engine: "aria2", EngineRef: ptr("2089b05ecca3d829"), State: store.Active,
			Name: "debian.iso", URIs: []string{"https://example.com/debian.iso"}, Dir: "/dl", TotalBytes: ptr(int64(4 << 30)),
			DoneBytes: ptr(int64(1 << 30)), Options: map[string]string{"header": "***"}, Queue: "default", CreatedAt: at,
			SpeedDown: 1 << 20, ETASeconds: ptr(int64(3072)), Label: "downloading", Actions: []string{"pause", "remove", "refresh", "open_folder", "limit"}},
		{ID: "01JC0000000000000000000002", Num: 2, Kind: "magnet", Engine: "aria2", State: store.Error, ErrorKind: ptr("not-found"),
			ErrorCode: ptr(3), ErrorMsg: ptr("404"), Name: "x", URIs: []string{"magnet:?xt=urn:btih:abc"}, Dir: "/dl",
			Options: map[string]string{}, Queue: "default", CreatedAt: at},
	}
}

func TestListJSONGolden(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, out, errOut := run(t, List, "", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("not JSON: %s", out)
	}
	golden(t, "list.golden", out)
}

func TestListHuman(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, out, _ := run(t, List, "")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"#1 ", "#2 ", "debian.iso", "25%", "4.0 GiB", "error", "404"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}
}

func TestStatusJSON(t *testing.T) {
	startFake(t)
	code, out, _ := run(t, Status, "", "--json")
	if code != 0 {
		t.Fatal(code)
	}
	golden(t, "status.golden", out)
}

// A removed job matches no prefix, and `list --all` does not show it
// either, so the hint must not send the user there to find it.
func TestIDPrefixNoMatchHint(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, _, errOut := run(t, Remove, "", "01JD")
	if code != 2 || !strings.Contains(errOut, "removed jobs are not listed") {
		t.Fatalf("exit %d, stderr %q; want 2 and a hint that removed jobs are not listed", code, errOut)
	}
}

func TestIDPrefixAmbiguous(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, _, errOut := run(t, Pause, "", "01JC0")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	for _, j := range f.jobs {
		if !strings.Contains(errOut, j.ID) {
			t.Errorf("stderr %q should list %s", errOut, j.ID)
		}
	}
	if code, _, _ := run(t, Pause, "", "01J"); code != 2 {
		t.Errorf("3-char prefix: exit %d, want 2", code)
	}
	if code, _, errOut := run(t, Pause, "", "01jc0000000000000000000002"); code != 0 || len(f.paused) != 1 || f.paused[0] != f.jobs[1].ID {
		t.Errorf("unique prefix: exit %d paused %v: %s", code, f.paused, errOut)
	}
	// The daemon matches the prefixes; the whole history is never fetched.
	if want := []string{"01JC0000000000000000000002"}; !slices.Equal(f.snapPrefixes, want) {
		t.Errorf("snapshot prefixes %v, want %v", f.snapPrefixes, want)
	}
	if code, _, errOut := run(t, Pause, "", "01JZZ"); code != 2 || !strings.Contains(errOut, `no job id starts with "01JZZ"`) {
		t.Errorf("unknown prefix: exit %d: %s", code, errOut)
	}
}

// pause and resume say so when a job is left as it was, and still exit 0.
func TestPauseResumeSayWhatHappened(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs() // 1 active, 2 failed
	done := *f.jobs[0]
	done.ID, done.Num, done.State = "01JC0000000000000000000003", 3, store.Done
	f.jobs = append(f.jobs, &done)
	for _, tc := range []struct {
		cmd  func(context.Context, []string, io.Writer, io.Writer) int
		id   string
		want string
	}{
		{Pause, "01JC0000000000000000000001", "paused #1\n"},
		{Pause, "01JC0000000000000000000002", "#2 has failed; nothing to pause\n"},
		{Pause, "01JC0000000000000000000003", "#3 is already finished; nothing to pause\n"},
		{Resume, "01JC0000000000000000000001", "#1 is already running; nothing to resume\n"},
		{Resume, "01JC0000000000000000000002", "resumed #2\n"},
		{Resume, "01JC0000000000000000000003", "#3 is already finished; nothing to resume\n"},
	} {
		if code, out, errOut := run(t, tc.cmd, "", tc.id); code != 0 || out != tc.want {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want 0 and %q", tc.id, code, out, errOut, tc.want)
		}
	}
}

func TestRemoveDeleteFilesAfterIDs(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, _, errOut := run(t, Remove, "", "01JC0000000000000000000001", "--delete-files")
	if code != 0 || !f.deleted || len(f.removed) != 1 {
		t.Fatalf("exit %d removed %v deleted %v: %s", code, f.removed, f.deleted, errOut)
	}
}

func TestListMasksUserinfo(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	f.jobs[0].URIs = []string{"https://bob:hunter2@example.com/debian.iso"}
	for _, args := range [][]string{nil, {"--json"}} {
		_, out, _ := run(t, List, "", args...)
		if strings.Contains(out, "hunter2") || strings.Contains(out, "bob") || (args != nil && !strings.Contains(out, "https://***@example.com/debian.iso")) {
			t.Errorf("list %v leaks credentials:\n%s", args, out)
		}
	}
}

func TestListMasksUnparseableUserinfo(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	f.jobs[0].URIs = []string{"https://alice:p%zzword@example.com/f"}
	_, out, _ := run(t, List, "", "--json")
	if strings.Contains(out, "p%zzword") || strings.Contains(out, "alice") || !strings.Contains(out, "https://***@example.com/f") {
		t.Errorf("list --json leaks credentials:\n%s", out)
	}
}

func TestListLimit(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	if code, _, errOut := run(t, List, "", "--all", "--limit", "5"); code != 0 || f.snapLimit != 5 {
		t.Fatalf("exit %d, limit sent %d: %s", code, f.snapLimit, errOut)
	}
	if code, _, _ := run(t, List, "", "--limit", "-1"); code != 2 {
		t.Fatalf("negative limit: exit %d, want 2", code)
	}
}
