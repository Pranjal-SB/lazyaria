package store

import "testing"

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
