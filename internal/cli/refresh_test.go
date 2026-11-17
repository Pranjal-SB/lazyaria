package cli

import (
	"slices"
	"strings"
	"testing"
)

func TestRefresh(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	code, out, errOut := run(t, Refresh, "", "01jc0000000000000000000001", "https://example.com/new.iso")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if want := []string{f.jobs[0].ID, "https://example.com/new.iso"}; !slices.Equal(f.refreshed, want) {
		t.Fatalf("refreshed %v, want %v", f.refreshed, want)
	}
	if !strings.Contains(out, "refreshed "+f.jobs[0].ID) {
		t.Errorf("stdout %q", out)
	}
	for _, args := range [][]string{{"01JC0000000000000000000001"}, {"01JC0000000000000000000001", "https://a/b", "https://a/c"}, {"01J", "https://a/b"}} {
		if code, _, errOut := run(t, Refresh, "", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2: %s", args, code, errOut)
		}
	}
}
