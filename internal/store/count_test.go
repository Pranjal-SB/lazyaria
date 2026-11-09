package store

import (
	"testing"
	"time"
)

func TestCountJobs(t *testing.T) {
	s := open(t)
	for _, st := range []State{Active, Active, Queued, Done, Paused} {
		if err := s.InsertJob(ctx, &Job{ID: NewID(), Kind: "http", Engine: "aria2", State: st, Name: "n", Dir: "/d", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.CountJobs(ctx, Active, Queued, Seeding)
	if err != nil || n != 3 {
		t.Fatalf("CountJobs = %d, %v; want 3", n, err)
	}
	s.Close()
	if _, err := s.CountJobs(ctx, Active); err == nil {
		t.Fatal("CountJobs on a closed store returned no error")
	}
}
