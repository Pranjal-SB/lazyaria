package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func nums(js []*Job) []int64 {
	var out []int64
	for _, j := range js {
		out = append(out, j.Num)
	}
	return out
}

func TestJobNumbersSequential(t *testing.T) {
	s := open(t)
	var ids []string
	for i, name := range []string{"a", "b", "c"} {
		j := job(name, Queued)
		if err := s.InsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		if j.Num != int64(i+1) {
			t.Fatalf("insert %d set Num %d", i, j.Num)
		}
		ids = append(ids, j.ID)
	}
	for i, id := range ids {
		got, err := s.GetJob(ctx, id)
		if err != nil || got.Num != int64(i+1) {
			t.Fatalf("GetJob(%d) = %+v, %v", i, got, err)
		}
	}
	list, err := s.ListJobs(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := nums(list); !slices.Equal(got, []int64{1, 2, 3}) {
		t.Fatalf("ListJobs nums %v", got)
	}
}

// A counter, not max(num)+1: deleting the highest row must not free its number.
func TestJobNumbersNeverReused(t *testing.T) {
	s := open(t)
	var last *Job
	for _, name := range []string{"a", "b", "c"} {
		last = job(name, Queued)
		if err := s.InsertJob(ctx, last); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteJob(ctx, last.ID); err != nil {
		t.Fatal(err)
	}
	j := job("d", Queued)
	if err := s.InsertJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if j.Num != 4 {
		t.Fatalf("after deleting #3, new job got #%d, want 4", j.Num)
	}
}

func TestMigration0003Backfill(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	s, err := openWith(p, migrations(t, 2))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := s.version(); v != 2 {
		t.Fatalf("setup version %d, want 2", v)
	}
	// Ids out of insert order: numbers follow id order, the creation order.
	ids := []string{"01KB0000000000000000000003", "01KB0000000000000000000001", "01KB0000000000000000000002"}
	for _, id := range ids {
		_, err := s.db.Exec(`INSERT INTO jobs(id, kind, engine, state, name, uris, dir, created_at)
			VALUES(?,?,?,?,?,?,?,?)`, id, "http", "aria2", "queued", id, `["http://x/`+id+`"]`, "/tmp", time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.version(); v != 3 {
		t.Fatalf("version %d, want 3", v)
	}
	for i, id := range []string{ids[1], ids[2], ids[0]} {
		got, err := s.GetJob(ctx, id)
		if err != nil || got.Num != int64(i+1) {
			t.Fatalf("job %s = %+v, %v; want #%d", id, got, err, i+1)
		}
	}
	j := job("next", Queued)
	if err := s.InsertJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if j.Num != 4 {
		t.Fatalf("next insert got #%d, want 4", j.Num)
	}
}

func TestFilterNums(t *testing.T) {
	s := open(t)
	for _, st := range []State{Queued, Done, Active} {
		if err := s.InsertJob(ctx, job(string(st), st)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListJobs(ctx, Filter{Nums: []int64{2}, IncludeHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := nums(list); !slices.Equal(got, []int64{2}) {
		t.Fatalf("Filter{Nums: [2]} gave %v", got)
	}
}
