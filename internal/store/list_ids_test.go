package store

import (
	"slices"
	"testing"
)

func TestListJobsByIDs(t *testing.T) {
	s := open(t)
	a, b, c, gone := job("a", Active), job("b", Done), job("c", Queued), job("gone", Removed)
	for _, j := range []*Job{a, b, c, gone} {
		if err := s.InsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListJobs(ctx, Filter{IncludeHistory: true, IDs: []string{a.ID, b.ID, gone.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != a.ID || got[1].ID != b.ID {
		t.Fatalf("got %d jobs %v; want a and b, never a removed one", len(got), got)
	}
}

func TestListJobsByIDPrefix(t *testing.T) {
	s := open(t)
	a, b, c, gone := job("a", Active), job("b", Done), job("c", Queued), job("gone", Removed)
	a.ID, b.ID, c.ID, gone.ID = "01AAAA1", "01AAAB2", "01BBBB3", "01AAAA4"
	for _, j := range []*Job{a, b, c, gone} {
		if err := s.InsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListJobs(ctx, Filter{IncludeHistory: true, IDPrefixes: []string{"01AAA", "01BBBB3"}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, j := range got {
		ids = append(ids, j.ID)
	}
	if len(ids) != 3 || !slices.Contains(ids, a.ID) || !slices.Contains(ids, b.ID) || !slices.Contains(ids, c.ID) {
		t.Fatalf("got %v; want a, b and c, never the removed job", ids)
	}
}

// Far more ids than SQLite allows variables still work, with and without
// a limit.
func TestListJobsManyIDs(t *testing.T) {
	s := open(t)
	a, b := job("a", Active), job("b", Done)
	for _, j := range []*Job{a, b} {
		if err := s.InsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{a.ID, b.ID}
	for len(ids) < 33500 {
		ids = append(ids, NewID())
	}
	for limit, want := range map[int]int{0: 2, 1: 1} {
		got, err := s.ListJobs(ctx, Filter{IncludeHistory: true, IDs: ids, Limit: limit})
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if len(got) != want || got[0].ID != a.ID {
			t.Fatalf("limit %d: got %d jobs, want %d starting with a", limit, len(got), want)
		}
	}
}
