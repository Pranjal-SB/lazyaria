package store

import (
	"errors"
	"testing"
)

func TestFindByEngineRef(t *testing.T) {
	s := open(t)
	ref, old := "g1", "g2"
	live, done := job("live", Paused), job("done", Done)
	live.EngineRef, done.EngineRef = &ref, &old
	for _, j := range []*Job{job("other", Active), live, done} {
		if err := s.InsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	if j, err := s.FindByEngineRef(ctx, ref); err != nil || j.ID != live.ID {
		t.Fatalf("FindByEngineRef(g1) = %v, %v; want the paused job", j, err)
	}
	// Finished and removed rows keep refs the engine no longer works on.
	if _, err := s.FindByEngineRef(ctx, old); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindByEngineRef(done job's ref) err = %v; want ErrNotFound", err)
	}
}
