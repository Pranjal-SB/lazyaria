package store

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

// migrations returns the embedded migrations numbered up to upTo.
func migrations(t *testing.T, upTo int) fstest.MapFS {
	t.Helper()
	base, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(base, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	m := fstest.MapFS{}
	for _, name := range names {
		if n, err := migrationNum(name); err != nil || n > upTo {
			continue
		}
		body, err := fs.ReadFile(base, name)
		if err != nil {
			t.Fatal(err)
		}
		m[name] = &fstest.MapFile{Data: body}
	}
	return m
}

func TestStagedAndPublishRoundTrip(t *testing.T) {
	s := open(t)
	j := job("a.iso", Active)
	j.Staged = true
	j.Publish = &Publish{Name: "a.1.iso", Temp: ".a.1.iso.laria-copy", Entries: []PublishEntry{
		{Rel: "a.iso", Size: 42, Ino: 7},
		{Rel: "sub", Size: 0, Ino: 1 << 63},
	}}
	if err := s.InsertJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	check := func(name string, got *Job) {
		t.Helper()
		if !got.Staged || !reflect.DeepEqual(got.Publish, j.Publish) {
			t.Fatalf("%s: staged %v publish %+v, want %+v", name, got.Staged, got.Publish, j.Publish)
		}
	}
	got, err := s.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	check("GetJob", got)
	list, err := s.ListJobs(ctx, Filter{IDs: []string{j.ID}})
	if err != nil || len(list) != 1 {
		t.Fatalf("ListJobs = %v, %v", list, err)
	}
	check("ListJobs", list[0])
	upd, err := s.UpdateJob(ctx, j.ID, func(x *Job) error { check("UpdateJob fn", x); return nil })
	if err != nil {
		t.Fatal(err)
	}
	check("UpdateJob result", upd)
	got, _ = s.GetJob(ctx, j.ID)
	check("GetJob after no-op update", got)

	if _, err := s.UpdateJob(ctx, j.ID, func(x *Job) error { x.Publish = nil; x.Staged = false; return nil }); err != nil {
		t.Fatal(err)
	}
	var raw *string
	if err := s.db.QueryRow(`SELECT publish FROM jobs WHERE id=?`, j.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != nil {
		t.Fatalf("publish column = %q, want NULL", *raw)
	}
	got, _ = s.GetJob(ctx, j.ID)
	if got.Staged || got.Publish != nil {
		t.Fatalf("after clear: staged %v publish %+v", got.Staged, got.Publish)
	}
}

// A fresh database runs every migration in one go; there is nothing to back up.
func TestFreshOpenMakesNoBackup(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if baks, _ := filepath.Glob(p + ".bak-v*"); len(baks) != 0 {
		t.Fatalf("fresh database left backups %v", baks)
	}
}

func TestMigration0002KeepsRows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.db")
	s, err := openWith(p, migrations(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := s.version(); v != 1 {
		t.Fatalf("setup version %d, want 1", v)
	}
	j := job("old.iso", Active)
	_, err = s.db.Exec(`INSERT INTO jobs(id, kind, engine, state, name, uris, dir, created_at)
		VALUES(?,?,?,?,?,?,?,?)`, j.ID, j.Kind, j.Engine, string(j.State), j.Name, `["http://x/old.iso"]`, j.Dir, j.CreatedAt.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Open runs every later migration too; GetJob needs their columns.
	if v, _ := s.version(); v < 2 {
		t.Fatalf("version %d, want at least 2", v)
	}
	got, err := s.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Staged || got.Publish != nil || got.Name != "old.iso" || got.State != Active {
		t.Fatalf("migrated row: %+v", got)
	}
	list, err := s.ListJobs(ctx, Filter{IDs: []string{j.ID}})
	if err != nil || len(list) != 1 || list[0].Staged || list[0].Publish != nil {
		t.Fatalf("ListJobs = %+v, %v", list, err)
	}
}
