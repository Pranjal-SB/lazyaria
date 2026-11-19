package fsutil

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s still exists (err %v)", path, err)
	}
}

// bothModes runs f with the platform's atomic rename and with the portable
// fallback other systems use.
func bothModes(t *testing.T, f func(t *testing.T)) {
	t.Run("atomic", f)
	t.Run("fallback", func(t *testing.T) {
		orig := renameAtomic
		renameAtomic = func(string, string) error { return errNoAtomic }
		t.Cleanup(func() { renameAtomic = orig })
		f(t)
	})
}

func TestRenameNoReplaceRefusesExisting(t *testing.T) {
	bothModes(t, func(t *testing.T) {
		d := t.TempDir()
		a, b := filepath.Join(d, "a"), filepath.Join(d, "b")
		write(t, a, "ours")
		write(t, b, "theirs")
		if err := RenameNoReplace(a, b); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("err = %v, want fs.ErrExist", err)
		}
		if read(t, a) != "ours" || read(t, b) != "theirs" {
			t.Fatal("a file changed")
		}
		// An empty directory is replaced by a plain rename(2); this must not be.
		empty := filepath.Join(d, "empty")
		if err := os.Mkdir(empty, 0o755); err != nil {
			t.Fatal(err)
		}
		src := filepath.Join(d, "src")
		write(t, filepath.Join(src, "f"), "x")
		if err := RenameNoReplace(src, empty); !errors.Is(err, fs.ErrExist) {
			t.Fatalf("dir onto empty dir: err = %v, want fs.ErrExist", err)
		}
		if read(t, filepath.Join(src, "f")) != "x" {
			t.Fatal("source dir changed")
		}
	})
}

func TestRenameNoReplaceMovesDirAtomically(t *testing.T) {
	bothModes(t, func(t *testing.T) {
		d := t.TempDir()
		src, dst := filepath.Join(d, "album"), filepath.Join(d, "up", "album")
		write(t, filepath.Join(src, "1.flac"), "one")
		write(t, filepath.Join(src, "2.flac"), "two")
		if err := os.Mkdir(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := RenameNoReplace(src, dst); err != nil {
			t.Fatal(err)
		}
		gone(t, src)
		if read(t, filepath.Join(dst, "1.flac")) != "one" || read(t, filepath.Join(dst, "2.flac")) != "two" {
			t.Fatal("moved dir lost content")
		}
	})
}

// tree records every entry under root: file bytes, symlink targets, modes.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		fi, err := e.Info()
		if err != nil {
			return err
		}
		v := fi.Mode().String()
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			v += " -> " + target
		case fi.Mode().IsRegular():
			v += " " + read(t, p)
		}
		m[rel] = v
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func equalTrees(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestCopyNoReplace(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "stage", "album")
	write(t, filepath.Join(src, "a.txt"), "alpha")
	write(t, filepath.Join(src, "sub", "b.bin"), string(bytes.Repeat([]byte{0, 1, 2, 255}, 70000)))
	write(t, filepath.Join(src, "sub", "deeper", "c"), "")
	if err := os.Chmod(filepath.Join(src, "a.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	want := tree(t, src)

	dl := filepath.Join(d, "dl")
	if err := os.Mkdir(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp, dst := filepath.Join(dl, ".album.laria-copy"), filepath.Join(dl, "album")
	if err := CopyNoReplace(src, tmp, dst); err != nil {
		t.Fatal(err)
	}
	gone(t, tmp)
	if got := tree(t, dst); !equalTrees(got, want) {
		t.Fatalf("copy differs:\n got %v\nwant %v", got, want)
	}
	if got := tree(t, src); !equalTrees(got, want) {
		t.Fatal("source changed")
	}

	// A single file.
	if err := CopyNoReplace(filepath.Join(src, "a.txt"), filepath.Join(dl, ".a.laria-copy"), filepath.Join(dl, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dl, "a.txt")); fi == nil || fi.Mode().Perm() != 0o600 || read(t, filepath.Join(dl, "a.txt")) != "alpha" {
		t.Fatalf("single file copy: %v", fi)
	}

	// dst taken: refused, the temp copy is cleaned up, nothing else changes.
	before := tree(t, dst)
	tmp2 := filepath.Join(dl, ".album.laria-copy2")
	if err := CopyNoReplace(src, tmp2, dst); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	gone(t, tmp2)
	if !equalTrees(tree(t, dst), before) || !equalTrees(tree(t, src), want) {
		t.Fatal("a refused copy changed files")
	}

	// tmp taken (not ours): refused and left alone.
	write(t, tmp2, "someone else")
	if err := CopyNoReplace(src, tmp2, filepath.Join(dl, "other")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("taken tmp: err = %v, want fs.ErrExist", err)
	}
	if read(t, tmp2) != "someone else" {
		t.Fatal("a taken tmp was changed")
	}
	gone(t, filepath.Join(dl, "other"))
}

// overlayfs refuses to rename a folder (EXDEV) even within one folder; the
// copy then builds dst and moves the files into it one by one.
func TestCopyNoReplaceFolderRenameRefused(t *testing.T) {
	orig := renameAtomic
	renameAtomic = func(old, new string) error {
		if fi, err := os.Lstat(old); err == nil && fi.IsDir() {
			return syscall.EXDEV
		}
		return orig(old, new)
	}
	t.Cleanup(func() { renameAtomic = orig })
	d := t.TempDir()
	src := filepath.Join(d, "album")
	write(t, filepath.Join(src, "a"), "alpha")
	write(t, filepath.Join(src, "sub", "b"), "beta")
	if err := os.Symlink("a", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	want := tree(t, src)
	tmp, dst := filepath.Join(d, ".album.tmp"), filepath.Join(d, "up")
	if err := CopyNoReplace(src, tmp, dst); err != nil {
		t.Fatal(err)
	}
	gone(t, tmp)
	if got := tree(t, dst); !equalTrees(got, want) {
		t.Fatalf("copy differs:\n got %v\nwant %v", got, want)
	}
	// dst taken: refused, nothing half-built left behind.
	tmp2 := filepath.Join(d, ".album.tmp2")
	if err := CopyNoReplace(src, tmp2, dst); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	gone(t, tmp2)
	if got := tree(t, dst); !equalTrees(got, want) {
		t.Fatal("a refused copy changed dst")
	}
}

// MoveInto builds dst file by file. Resumed, it continues into the dst it
// made; a failure leaves dst as it is, since anything in it may matter.
func TestMoveInto(t *testing.T) {
	d := t.TempDir()
	src, dst := filepath.Join(d, "tmp"), filepath.Join(d, "album")
	write(t, filepath.Join(src, "a"), "alpha")
	write(t, filepath.Join(src, "sub", "b"), "beta")
	write(t, filepath.Join(src, "c"), "gamma")
	// A first run that moved only a, then stopped.
	write(t, filepath.Join(dst, "a"), "alpha")
	os.Remove(filepath.Join(src, "a"))
	if err := MoveInto(src, dst, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("fresh build onto an existing folder: err = %v, want fs.ErrExist", err)
	}
	if err := MoveInto(src, dst, true); err != nil {
		t.Fatal(err)
	}
	gone(t, src)
	if read(t, filepath.Join(dst, "a")) != "alpha" || read(t, filepath.Join(dst, "sub", "b")) != "beta" || read(t, filepath.Join(dst, "c")) != "gamma" {
		t.Fatalf("dst holds %v", tree(t, dst))
	}

	// A clash halfway: the error comes back and nothing already built goes.
	src2, dst2 := filepath.Join(d, "tmp2"), filepath.Join(d, "album2")
	write(t, filepath.Join(src2, "a"), "alpha")
	write(t, filepath.Join(src2, "b"), "beta")
	write(t, filepath.Join(dst2, "b"), "a scanner's")
	if err := MoveInto(src2, dst2, true); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("clash: err = %v, want fs.ErrExist", err)
	}
	if read(t, filepath.Join(dst2, "a")) != "alpha" || read(t, filepath.Join(dst2, "b")) != "a scanner's" {
		t.Fatalf("dst2 holds %v", tree(t, dst2))
	}
}

func TestLinkUnsupported(t *testing.T) {
	for _, e := range []syscall.Errno{syscall.EPERM, syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.EXDEV, syscall.EMLINK} {
		if !LinkUnsupported(&os.LinkError{Op: "link", Old: "a", New: "b", Err: e}) {
			t.Errorf("%v: want true", e)
		}
	}
	if LinkUnsupported(&os.LinkError{Op: "link", Old: "a", New: "b", Err: syscall.ENOENT}) || LinkUnsupported(nil) {
		t.Error("ENOENT or nil reported as unsupported")
	}
}

func TestIsCrossDevice(t *testing.T) {
	if !IsCrossDevice(&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EXDEV}) {
		t.Error("EXDEV not cross-device")
	}
	if IsCrossDevice(&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.EEXIST}) || IsCrossDevice(nil) {
		t.Error("EEXIST or nil reported as cross-device")
	}
}

func TestFileID(t *testing.T) {
	d := t.TempDir()
	a, b, c := filepath.Join(d, "a"), filepath.Join(d, "b"), filepath.Join(d, "c")
	write(t, a, "12345")
	write(t, c, "12345")
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	ia, da, sa, err := FileID(a)
	if err != nil {
		t.Fatal(err)
	}
	ib, db, sb, _ := FileID(b)
	ic, _, _, _ := FileID(c)
	if ia == 0 || ia != ib || da == 0 || da != db || sa != 5 || sb != 5 {
		t.Fatalf("a=(%d,%d) b=(%d,%d)", ia, sa, ib, sb)
	}
	if ic == ia {
		t.Fatal("different files share an inode")
	}
	if _, _, sd, err := FileID(d); err != nil || sd != 0 {
		t.Fatalf("dir size %d, err %v", sd, err)
	}
	if _, _, _, err := FileID(filepath.Join(d, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: err %v", err)
	}
}

func TestSyncDir(t *testing.T) {
	if err := SyncDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := SyncDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing dir: want error")
	}
}
