package trash

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

func TestRestoreNeverOverwritesRace(t *testing.T) {
	work := setup(t)
	p := filepath.Join(work, "f")
	write(t, p, "old")
	e, err := Trash(p)
	if err != nil {
		t.Fatal(err)
	}
	// A file appears after the existence check, before the move.
	beforeMove = func() { write(t, p, "racer") }
	t.Cleanup(func() { beforeMove = func() {} })
	if err := Restore(e); err == nil {
		t.Fatal("expected error")
	}
	if b, _ := os.ReadFile(p); string(b) != "racer" {
		t.Fatalf("overwritten: %q", b)
	}
	if _, err := os.Stat(e.TrashedPath); err != nil {
		t.Fatal("trashed copy lost")
	}
}

func TestRestoreFallbacks(t *testing.T) {
	work := setup(t)
	for _, dir := range []bool{false, true} {
		p := filepath.Join(work, "fb")
		if dir {
			p += "d"
			os.Mkdir(p, 0o755)
		} else {
			write(t, p, "old")
		}
		e, err := Trash(p)
		if err != nil {
			t.Fatal(err)
		}
		linkMode = true
		err = Restore(e)
		linkMode = false
		if err != nil {
			t.Fatalf("dir=%v: %v", dir, err)
		}
		if _, err := os.Lstat(p); err != nil {
			t.Fatal(err)
		}
		// restore again over an existing target must fail in fallback mode too
		e2, err := Trash(p)
		if err != nil {
			t.Fatal(err)
		}
		if dir {
			os.Mkdir(p, 0o755)
		} else {
			write(t, p, "new")
		}
		linkMode = true
		err = Restore(e2)
		linkMode = false
		if err == nil {
			t.Fatalf("dir=%v: fallback overwrote", dir)
		}
		os.RemoveAll(p)
	}
}

func TestRestoreInfoAlreadyGone(t *testing.T) {
	work := setup(t)
	p := filepath.Join(work, "f")
	write(t, p, "x")
	e, err := Trash(p)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(e.InfoPath)
	if err := Restore(e); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p); err != nil {
		t.Fatal(err)
	}
}

func TestTrashCrossDeviceIsNoTrash(t *testing.T) {
	work := setup(t)
	p := filepath.Join(work, "f")
	write(t, p, "x")
	renameFn = func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EXDEV} }
	t.Cleanup(func() { renameFn = moveNoReplace })
	if _, err := Trash(p); !errors.Is(err, ErrNoTrash) {
		t.Fatalf("want ErrNoTrash, got %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("original must stay")
	}
	infos, _ := filepath.Glob(filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "info", "*"))
	if len(infos) != 0 {
		t.Fatalf("stray trashinfo: %v", infos)
	}
}

// $topdir/.Trash-$uid is used only as a real directory of the user's own:
// a symlink or a file planted there is refused, never followed.
func TestTopTrashRefusesPlanted(t *testing.T) {
	name := ".Trash-" + strconv.Itoa(os.Getuid())
	top := t.TempDir()
	dir, err := topTrash(top)
	if err != nil || dir != filepath.Join(top, name) {
		t.Fatalf("topTrash on a fresh drive = %q, %v", dir, err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "files")); err != nil || !fi.IsDir() {
		t.Fatalf("files/ not made: %v", err)
	}
	elsewhere := t.TempDir()
	for _, plant := range []func(p string) error{
		func(p string) error { return os.Symlink(elsewhere, p) },
		func(p string) error { return os.WriteFile(p, nil, 0o600) },
	} {
		top := t.TempDir()
		if err := plant(filepath.Join(top, name)); err != nil {
			t.Fatal(err)
		}
		if _, err := topTrash(top); !errors.Is(err, ErrNoTrash) {
			t.Fatalf("planted %s: err %v, want ErrNoTrash", name, err)
		}
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
		t.Fatalf("followed the symlink: %v", ents)
	}
}

func TestMountTopResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ln")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(real)
	dev, _ := device(fi)
	top := mountTop(link, dev)
	if r, _ := filepath.EvalSymlinks(top); r != top {
		t.Fatalf("top %q is not a real path", top)
	}
}

func TestTrashInfoPathStaysInsideTop(t *testing.T) {
	work := setup(t)
	real := filepath.Join(work, "real")
	os.Mkdir(real, 0o755)
	link := filepath.Join(work, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(real, "f"), "x")
	e, err := Trash(filepath.Join(link, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Original != filepath.Join(real, "f") {
		t.Fatalf("original %q", e.Original)
	}
	info, _ := os.ReadFile(e.InfoPath)
	if !strings.Contains(string(info), "Path="+filepath.ToSlash(real)+"/f") {
		t.Fatalf("path not resolved:\n%s", info)
	}
	if err := Restore(e); err != nil {
		t.Fatal(err)
	}
}

func TestCheck(t *testing.T) {
	work := setup(t)
	p := filepath.Join(work, "f")
	write(t, p, "x")
	if err := Check(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p); err != nil {
		t.Fatal("Check moved the file")
	}
	if err := Check(filepath.Join(work, "nope")); !os.IsNotExist(err) {
		t.Fatalf("want not-exist, got %v", err)
	}
}

// A trashed name taken between the check and the move is never replaced;
// the item goes under the next name.
func TestTrashNeverReplacesRacingName(t *testing.T) {
	work := setup(t)
	p := filepath.Join(work, "r")
	write(t, p, "mine")
	renameFn = func(src, dst string) error {
		renameFn = moveNoReplace
		write(t, dst, "racer") // appears just before the move
		return moveNoReplace(src, dst)
	}
	t.Cleanup(func() { renameFn = moveNoReplace })
	e, err := Trash(p)
	if err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(os.Getenv("XDG_DATA_HOME"), "Trash", "files")
	if b, _ := os.ReadFile(filepath.Join(files, "r")); string(b) != "racer" {
		t.Fatalf("racing file replaced: %q", b)
	}
	if b, _ := os.ReadFile(e.TrashedPath); filepath.Base(e.TrashedPath) != "r.2" || string(b) != "mine" {
		t.Fatalf("trashed at %s: %q", e.TrashedPath, b)
	}
}

// The move into the trash must never replace what is already at the
// destination; a plain os.Rename as the default would.
func TestDefaultRenameNeverReplaces(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("src"), 0o600)
	os.WriteFile(dst, []byte("dst"), 0o600)
	if err := renameFn(src, dst); !errors.Is(err, os.ErrExist) {
		t.Fatalf("renameFn onto an existing file: err = %v, want ErrExist", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "dst" {
		t.Fatalf("destination replaced: %q", b)
	}
}
