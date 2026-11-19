package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

var ctx = context.Background()

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func job(name string, st State) *Job {
	return &Job{ID: NewID(), Kind: "http", Engine: "aria2", State: st, Name: name,
		URIs: []string{"http://x/" + name}, Dir: "/tmp", Options: map[string]string{},
		Queue: "main", CreatedAt: time.UnixMilli(time.Now().UnixMilli())}
}

func TestOpenCreates0600(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	os.Chmod(p, 0o644)
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	fi, _ = os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode after reopen %v", fi.Mode().Perm())
	}
}

func TestMigrationBackup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	j := job("keep", Queued)
	s.InsertJob(ctx, j) // sits in the WAL until checkpoint
	cur, _ := s.version()
	s.Close()
	next := cur + 1
	m := migrations(t, cur)
	m[fmt.Sprintf("%04d_x.sql", next)] = &fstest.MapFile{Data: []byte("CREATE TABLE x(a);")}
	s, err = openWith(p, m)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bak := fmt.Sprintf("%s.bak-v%d", p, next)
	if _, err := os.Stat(bak); err != nil {
		t.Fatal(err)
	}
	bs, err := Open(bak)
	if err != nil {
		t.Fatal(err)
	}
	defer bs.Close()
	if _, err := bs.GetJob(ctx, j.ID); err != nil {
		t.Fatalf("backup lacks row: %v", err)
	}
	if v, _ := s.version(); v != next {
		t.Fatalf("version %d, want %d", v, next)
	}
}

func TestBackupKeepsLastTwo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for n := 2; n <= 4; n++ {
		if err := s.backup(p, n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(p + ".bak-v2"); err == nil {
		t.Fatal("v2 should be pruned")
	}
	for _, n := range []int{3, 4} {
		if _, err := os.Stat(fmt.Sprintf("%s.bak-v%d", p, n)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	s, _ := Open(p)
	s.db.Exec(`UPDATE meta SET value='99' WHERE key='schema_version'`)
	s.Close()
	_, err := Open(p)
	var ns ErrNewerSchema
	if !errors.As(err, &ns) {
		t.Fatalf("got %v", err)
	}
	if err.Error() != "database schema v99 is newer than this lazyaria; upgrade lazyaria" {
		t.Fatal(err.Error())
	}
}

func TestInsertGetRoundTrip(t *testing.T) {
	s := open(t)
	ms := func(n int64) *time.Time { x := time.UnixMilli(n); return &x }
	str := func(v string) *string { return &v }
	i64 := func(v int64) *int64 { return &v }
	dev := uint64(1 << 63)
	code := 7
	j := &Job{ID: NewID(), Kind: "torrent", Engine: "aria2", EngineRef: str("gid1"), State: Error,
		ErrorKind: str("net"), ErrorCode: &code, ErrorMsg: str("boom"), Name: "n",
		URIs: []string{"a", "b"}, Torrent: []byte{0, 1, 2}, Dir: "/d", DirDev: &dev,
		TotalBytes: i64(100), DoneBytes: i64(50), Options: map[string]string{"k": "v"},
		LimitDown: 5, LimitUp: 6, Category: str("c"), Queue: "q", Priority: 3, RetryCount: 2,
		RetryAt: ms(10), Referrer: str("r"), CreatedAt: *ms(1), StartedAt: ms(2),
		FinishedAt: ms(3), RemovedAt: ms(4)}
	if err := s.InsertJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, j) {
		t.Fatalf("got %+v\nwant %+v", got, j)
	}
	if got.Out != nil {
		t.Fatal("Out should be nil")
	}
	if _, err := s.GetJob(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestUpdateJobAtomic(t *testing.T) {
	s := open(t)
	j := job("a", Queued)
	s.InsertJob(ctx, j)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.UpdateJob(ctx, j.ID, func(j *Job) error { j.RetryCount++; return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, _ := s.GetJob(ctx, j.ID)
	if got.RetryCount != 50 {
		t.Fatalf("got %d", got.RetryCount)
	}
}

func TestListOrderSmart(t *testing.T) {
	s := open(t)
	fin := func(n string, ms int64) *Job {
		j := job(n, Done)
		t := time.UnixMilli(ms)
		j.FinishedAt = &t
		return j
	}
	for _, j := range []*Job{fin("doneold", 1000), job("queued", Queued), job("active", Active),
		fin("donenew", 2000), job("error", Error)} {
		if err := s.InsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	gone := job("gone", Removed)
	s.InsertJob(ctx, gone)
	got, err := s.ListJobs(ctx, Filter{IncludeHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, j := range got {
		names = append(names, j.Name)
	}
	want := []string{"error", "active", "queued", "donenew", "doneold"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v", names)
	}
	live, _ := s.ListJobs(ctx, Filter{})
	if len(live) != 3 {
		t.Fatalf("without history: %d", len(live))
	}
	one, _ := s.ListJobs(ctx, Filter{States: []State{Active}})
	if len(one) != 1 || one[0].Name != "active" {
		t.Fatalf("state filter: %v", one)
	}
}

func TestSearchEscapes(t *testing.T) {
	s := open(t)
	s.InsertJob(ctx, job("a%b", Queued))
	s.InsertJob(ctx, job("ab", Queued))
	got, err := s.Search(ctx, "%", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "a%b" {
		t.Fatalf("got %v", got)
	}
	got, _ = s.Search(ctx, "_", 10, 0)
	if len(got) != 0 {
		t.Fatalf("underscore matched %d", len(got))
	}
}

func TestFindByNormalizedURL(t *testing.T) {
	s := open(t)
	j := job("a", Queued)
	j.URIs = []string{"http://h/a", "http://h/b"}
	s.InsertJob(ctx, j)
	got, err := s.FindByNormalizedURL(ctx, "http://h/b")
	if err != nil || got.ID != j.ID {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := s.FindByNormalizedURL(ctx, "http://h"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestSettingsOverrides(t *testing.T) {
	s := open(t)
	s.SetSetting(ctx, "a", "1")
	s.SetSetting(ctx, "a", "2")
	s.SetSetting(ctx, "b", "3")
	m, _ := s.All()
	if !reflect.DeepEqual(m, map[string]string{"a": "2", "b": "3"}) {
		t.Fatal(m)
	}
	s.DeleteSetting(ctx, "a")
	m, _ = s.All()
	if len(m) != 1 || m["b"] != "3" {
		t.Fatal(m)
	}
}

func TestEvents(t *testing.T) {
	s := open(t)
	j := job("a", Queued)
	s.InsertJob(ctx, j)
	s.AddEvent(ctx, j.ID, "added", "x")
	s.AddEvent(ctx, j.ID, "renamed", "y")
	ev, err := s.Events(ctx, j.ID)
	if err != nil || len(ev) != 2 || ev[0].Kind != "added" || ev[1].Detail != "y" {
		t.Fatalf("%v %v", ev, err)
	}
}

func TestPurgeRemoved(t *testing.T) {
	s := open(t)
	old, fresh, live := job("old", Removed), job("fresh", Removed), job("live", Done)
	o, f := time.UnixMilli(1000), time.UnixMilli(9000)
	old.RemovedAt, fresh.RemovedAt = &o, &f
	for _, j := range []*Job{old, fresh, live} {
		s.InsertJob(ctx, j)
	}
	s.AddEvent(ctx, old.ID, "x", "")
	n, err := s.PurgeRemoved(ctx, time.UnixMilli(5000))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := s.GetJob(ctx, old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("old still there")
	}
	if ev, _ := s.Events(ctx, old.ID); len(ev) != 0 {
		t.Fatal("events not cascaded")
	}
	if _, err := s.GetJob(ctx, fresh.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFindByNormalizedURLNormalizes(t *testing.T) {
	s := open(t)
	j := job("a", Queued)
	j.URIs = []string{"http://host/x?a=2&b=1"}
	s.InsertJob(ctx, j)
	norm, err := textutil.NormalizeURL("HTTP://Host:80/x?b=1&a=2")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.FindByNormalizedURL(ctx, norm)
	if err != nil || got.ID != j.ID {
		t.Fatalf("got %v %v", got, err)
	}
	if got.URIs[0] != "http://host/x?a=2&b=1" {
		t.Fatal("raw uri changed")
	}
}

func TestSearchURIsNotJSON(t *testing.T) {
	s := open(t)
	j := job("a", Queued)
	j.URIs = []string{"http://h/p?a=1&b=2", "http://h/q"}
	s.InsertJob(ctx, j)
	for q, want := range map[string]int{"1&b": 1, "[": 0, `","`: 0} {
		got, err := s.Search(ctx, q, 10, 0)
		if err != nil || len(got) != want {
			t.Fatalf("%q: %d %v", q, len(got), err)
		}
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	s, _ := Open(p)
	cur, _ := s.version()
	s.Close()
	m := migrations(t, cur)
	m[fmt.Sprintf("%04d_bad.sql", cur+1)] = &fstest.MapFile{Data: []byte("CREATE TABLE partial(a); INSERT INTO nope VALUES(1);")}
	if _, err := openWith(p, m); err == nil {
		t.Fatal("want error")
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, _ := s.version(); v != cur {
		t.Fatalf("version %d, want %d", v, cur)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='partial'`).Scan(&n)
	if n != 0 {
		t.Fatal("partial table left behind")
	}
}

func TestPurgeDone(t *testing.T) {
	s := open(t)
	old, fresh, active := job("old", Done), job("fresh", Done), job("active", Active)
	o, f := time.UnixMilli(1000), time.UnixMilli(9000)
	old.FinishedAt, fresh.FinishedAt, active.FinishedAt = &o, &f, &o
	for _, j := range []*Job{old, fresh, active} {
		s.InsertJob(ctx, j)
	}
	n, err := s.PurgeDone(ctx, time.UnixMilli(5000))
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := s.GetJob(ctx, old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("old still there")
	}
}

func TestSchemaVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	if _, err := SchemaVersion(p); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing db: err %v", err)
	}
	if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("SchemaVersion created the database")
	}
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want, _ := s.version()
	if got, err := SchemaVersion(p); err != nil || got != want || got == 0 {
		t.Fatalf("SchemaVersion = %d, %v; want %d", got, err, want)
	}
}
