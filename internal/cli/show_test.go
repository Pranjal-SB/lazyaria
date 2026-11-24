package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

func sampleDetails(f *fake) {
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	f.details = daemon.Details{
		Files:    []daemon.DetailFile{{Path: "/dl/debian.iso", Length: 4 << 30, Done: 1 << 30, Selected: true}},
		Servers:  []string{"https://example.com/debian.iso"},
		Segments: []int{100, 100, 40, 0, 0, 0, 0, 0},
		Pieces:   2048,
		Events:   []daemon.DetailEvent{{At: at, Kind: "added"}, {At: at.Add(time.Minute), Kind: "active"}},
	}
}

func TestShowGolden(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	sampleDetails(f)
	code, out, errOut := run(t, Show, "", "1")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	golden(t, "show.golden", out)
	code, out, errOut = run(t, Show, "", "--json", "1")
	if code != 0 || !json.Valid([]byte(out)) {
		t.Fatalf("exit %d, stdout %q: %s", code, out, errOut)
	}
	golden(t, "show.json", out)
}

func TestShowByNumber(t *testing.T) {
	f := startFake(t)
	f.jobs = sampleJobs()
	sampleDetails(f)
	if code, out, errOut := run(t, Show, "", "#2"); code != 0 || !strings.HasPrefix(out, "#2 x\n") {
		t.Fatalf("exit %d, stdout %q: %s", code, out, errOut)
	}
	if f.detailsID != f.jobs[1].ID {
		t.Fatalf("details asked for %q, want job #2 %s", f.detailsID, f.jobs[1].ID)
	}
	if code, _, _ := run(t, Show, "", "1", "2"); code != 2 {
		t.Fatalf("two jobs: exit %d, want 2", code)
	}
}
