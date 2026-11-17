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
