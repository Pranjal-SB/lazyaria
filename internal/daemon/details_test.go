package daemon

import (
	"slices"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestSegments(t *testing.T) {
	for _, tc := range []struct {
		bits          string
		pieces, cells int
		want          []int
	}{
		{"ff", 8, 4, []int{100, 100, 100, 100}},
		{"f0", 8, 2, []int{100, 0}},
		{"c0", 2, 4, []int{100, 100, 100, 100}}, // more cells than pieces
		{"", 0, 10, nil},
		{"8", 3, 3, []int{100, 0, 0}}, // trailing bits ignored
		{"c0", 8, 2, []int{50, 0}},
		{"f", 8, 2, []int{100, 0}}, // a short bitfield: the rest is not done
	} {
		if got := Segments(tc.bits, tc.pieces, tc.cells); !slices.Equal(got, tc.want) {
			t.Errorf("Segments(%q, %d, %d) = %v, want %v", tc.bits, tc.pieces, tc.cells, got, tc.want)
		}
	}
}

func FuzzSegments(f *testing.F) {
	f.Add("ff", 8, 4)
	f.Add("c0", 2, 4)
	f.Add("zz", 3, 7)
	f.Fuzz(func(t *testing.T, bits string, pieces, cells int) {
		pieces, cells = pieces%(1<<20), cells%513 // aria2's and Details' bounds
		got := Segments(bits, pieces, cells)
		if pieces <= 0 || cells <= 0 {
			return
		}
		if len(got) != cells {
			t.Fatalf("len %d, want %d", len(got), cells)
		}
		for _, v := range got {
			if v < 0 || v > 100 {
				t.Fatalf("value %d out of 0..100: %v", v, got)
			}
		}
	})
}

func TestDetailsDoneJob(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	if _, err := r.st.UpdateJob(bg, j.ID, func(x *store.Job) error { x.State = store.Done; return nil }); err != nil {
		t.Fatal(err)
	}
	d, err := r.m.Details(bg, j.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Segments, []int{100, 100, 100, 100, 100}) || d.Job == nil || d.Job.Label != "done" {
		t.Fatalf("done job details: segments %v job %+v", d.Segments, d.Job)
	}
	if n := r.e.detailsCalls; n != 0 {
		t.Fatalf("asked the engine %d times about a finished job", n)
	}
	if len(d.Events) == 0 || d.Events[0].Kind != "added" {
		t.Fatalf("events %+v, want the add first", d.Events)
	}
}

func TestDetailsStripsUserinfo(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.details[ref] = engine.EngineDetails{
		Servers:  []engine.EngineServer{{URI: "https://bob:hunter2@mirror.example.com/a.iso"}},
		Files:    []engine.EngineFile{{Path: "/d/a.iso", Length: 100, Done: 50, Selected: true}},
		Bitfield: "f0", Pieces: 8,
	}
	d, err := r.m.Details(bg, j.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Servers, []string{"https://mirror.example.com/a.iso"}) {
		t.Fatalf("servers %v", d.Servers)
	}
	if !slices.Equal(d.Segments, []int{100, 0}) || d.Pieces != 8 || len(d.Files) != 1 || d.Files[0].Done != 50 {
		t.Fatalf("details %+v", d)
	}
}

// A job the engine does not know, or one with no pieces yet, has no map.
func TestDetailsNoPieces(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	d, err := r.m.Details(bg, j.ID, 4)
	if err != nil || d.Segments != nil {
		t.Fatalf("segments %v, err %v; want none", d.Segments, err)
	}
}
