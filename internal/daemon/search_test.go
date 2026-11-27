package daemon

import (
	"slices"
	"testing"
)

func TestSearchDecorated(t *testing.T) {
	r := newRig(t)
	r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}, StartPaused: true})
	r.add(t, AddParams{URIs: []string{"http://example.com/b.iso"}})
	js, err := r.m.Search(bg, "a.iso", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 1 {
		t.Fatalf("%d results, want 1", len(js))
	}
	if js[0].Label != "paused" || !slices.Contains(js[0].Actions, ActResume) {
		t.Fatalf("result label %q actions %v, want paused with resume", js[0].Label, js[0].Actions)
	}
}
