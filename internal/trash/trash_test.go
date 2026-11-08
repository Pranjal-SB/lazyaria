package trash

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (work string) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	return t.TempDir()
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTrashHomeRoundTrip(t *testing.T) {
	work := setup(t)
	p := filepath.Join(work, "a file é.txt")
	write(t, p, "hi")

	e, err := Trash(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Fatalf("original still there: %v", err)
	}
	if _, err := os.Stat(e.TrashedPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.ReadFile(e.InfoPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(info)
	if !strings.HasPrefix(s, "[Trash Info]\n") || !strings.Contains(s, "DeletionDate=") {
		t.Fatalf("bad info:\n%s", s)
	}
	if !strings.Contains(s, "a%20file%20%C3%A9.txt\n") || strings.Contains(s, "a file") {
		t.Fatalf("path not percent-encoded:\n%s", s)
	}
	if !strings.Contains(s, "Path="+filepath.ToSlash(work)) {
		t.Fatalf("expected absolute Path for home trash:\n%s", s)
	}
	for _, d := range []string{"files", "info"} {
		st, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", d))
		if err != nil || st.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", d, st, err)
		}
	}

	if err := Restore(e); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "hi" {
		t.Fatalf("restored content %q", b)
	}
	if _, err := os.Stat(e.InfoPath); !os.IsNotExist(err) {
		t.Fatal("trashinfo not removed")
	}
}

func TestTrashNameCollision(t *testing.T) {
	work := setup(t)
	p := filepath.Join(work, "x.bin")
	var got []string
	for _, c := range []string{"1", "2", "3"} {
		write(t, p, c)
		e, err := Trash(p)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, filepath.Base(e.TrashedPath))
	}
	if strings.Join(got, ",") != "x.bin,x.bin.2,x.bin.3" {
		t.Fatalf("got %v", got)
	}
}

func TestTrashDirectory(t *testing.T) {
	work := setup(t)
	d := filepath.Join(work, "dir")
	if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(d, "sub", "f"), "z")
	e, err := Trash(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.TrashedPath, "sub", "f")); err != nil {
		t.Fatal(err)
	}
	if err := Restore(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "sub", "f")); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRefusesOverwrite(t *testing.T) {
	work := setup(t)
	p := filepath.Join(work, "f")
	write(t, p, "old")
	e, err := Trash(p)
	if err != nil {
		t.Fatal(err)
	}
	write(t, p, "new")
	if err := Restore(e); err == nil {
		t.Fatal("expected error")
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Fatalf("overwritten: %q", b)
	}
	if _, err := os.Stat(e.TrashedPath); err != nil {
		t.Fatal("trashed copy lost")
	}
}

func TestTrashMissingFile(t *testing.T) {
	work := setup(t)
	if _, err := Trash(filepath.Join(work, "nope")); err == nil || errors.Is(err, ErrNoTrash) {
		t.Fatalf("want plain not-exist error, got %v", err)
	}
}

func TestEncodePath(t *testing.T) {
	if got := encodePath("/a b/ü/100%"); got != "/a%20b/%C3%BC/100%25" {
		t.Fatal(got)
	}
}
