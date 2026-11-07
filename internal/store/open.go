// Package store is the SQLite source of truth for download jobs and history.
package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound is returned when a job does not exist.
var ErrNotFound = errors.New("not found")

// ErrNewerSchema is returned when the database was written by a newer lazyaria.
type ErrNewerSchema struct{ Version int }

func (e ErrNewerSchema) Error() string {
	return fmt.Sprintf("database schema v%d is newer than this lazyaria; upgrade lazyaria", e.Version)
}

// Store wraps the database handle.
type Store struct{ db *sql.DB }

// Open opens (creating if needed) the database at p and applies migrations.
func Open(p string) (*Store, error) {
	m, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	return openWith(p, m)
}

func openWith(p string, migs fs.FS) (*Store, error) {
	f, err := os.OpenFile(p, os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create database %s: %w (check the state dir permissions)", p, err)
	}
	f.Close()
	if err := os.Chmod(p, 0o600); err != nil {
		return nil, err
	}
	q := url.Values{"_pragma": {
		"journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(ON)", "busy_timeout(5000)",
	}}
	u := url.URL{Scheme: "file", Path: p, RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	// One connection: lazyaria has a single writer, and this keeps
	// read-modify-write transactions from deadlocking on lock upgrades.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(p, migs); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) version() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&n)
	if err != nil || n == 0 {
		return 0, err
	}
	var v string
	err = s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

func migrationNum(name string) (int, error) {
	n, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
	if err != nil {
		return 0, fmt.Errorf("bad migration name %q", name)
	}
	return n, nil
}

func (s *Store) migrate(p string, migs fs.FS) error {
	files, err := fs.Glob(migs, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	cur, err := s.version()
	if err != nil {
		return err
	}
	latest := 0
	if len(files) > 0 {
		if latest, err = migrationNum(files[len(files)-1]); err != nil {
			return err
		}
	}
	if cur > latest {
		return ErrNewerSchema{cur}
	}
	for _, name := range files {
		n, err := migrationNum(name)
		if err != nil {
			return err
		}
		if n <= cur {
			continue
		}
		if cur > 0 { // nothing worth saving on a fresh database
			if err := backup(p, n); err != nil {
				return fmt.Errorf("back up before migration %d: %w", n, err)
			}
		}
		body, err := fs.ReadFile(migs, name)
		if err != nil {
			return err
		}
		if err := s.apply(n, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w (restore %s.bak-v%d if needed)", name, err, p, n)
		}
		cur = n
	}
	return nil
}

func (s *Store) apply(n int, body string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(body); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('schema_version',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(n)); err != nil {
		return err
	}
	return tx.Commit()
}

// backup copies the database to p.bak-vN and prunes all but the newest two.
func backup(p string, n int) error {
	// Fold the WAL into the main file so the copy is complete.
	if db, err := sql.Open("sqlite", "file:"+p); err == nil {
		db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
		db.Close()
	}
	src, err := os.Open(p)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(p+".bak-v"+strconv.Itoa(n), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	prefix := filepath.Base(p) + ".bak-v"
	baks, _ := filepath.Glob(p + ".bak-v*")
	byVer := map[int]string{}
	var vers []int
	for _, b := range baks {
		if v, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(b), prefix)); err == nil {
			byVer[v] = b
			vers = append(vers, v)
		}
	}
	sort.Ints(vers)
	for len(vers) > 2 {
		os.Remove(byVer[vers[0]])
		vers = vers[1:]
	}
	return nil
}
