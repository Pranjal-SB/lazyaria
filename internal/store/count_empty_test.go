package store

import (
	"testing"
	"time"
)

func TestCountJobsNoStates(t *testing.T) {
	s := open(t)
	if err := s.InsertJob(ctx, &Job{ID: NewID(), Kind: "http", Engine: "aria2", State: Active, Name: "n", Dir: "/d", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountJobs(ctx); err != nil || n != 0 {
		t.Fatalf("CountJobs() = %d, %v; want 0, nil", n, err)
	}
}
