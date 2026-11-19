package fsutil

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// CopyNoReplace copies the file or directory tree src to dst through tmp,
// then renames tmp to dst with RenameNoReplace, so dst appears complete or
// not at all. tmp is created fresh (O_EXCL, Mkdir): if it already exists the
// copy is refused and tmp is left alone; after any later failure tmp is
// removed. Files and directories are fsynced, then the parent of dst. It
// returns an error matching fs.ErrExist if dst exists, and never deletes src.
// Symlinks are copied as symlinks; other special files are refused.
func CopyNoReplace(src, tmp, dst string) error {
	if err := CopyTree(src, tmp); err != nil {
		return err
	}
	if err := Place(tmp, dst); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// CopyTree creates dst fresh (O_EXCL, Mkdir) as a copy of the file or tree
// src and fsyncs it. If dst already exists it fails with an error matching
// fs.ErrExist and leaves dst alone; after any later failure it removes what
// it made. Symlinks are copied as symlinks; other special files are refused.
func CopyTree(src, dst string) error {
	made := false
	if err := copyEntry(src, dst, &made); err != nil {
		if made {
			os.RemoveAll(dst)
		}
		return err
	}
	return nil
}

// Place renames tmp to dst without replacing, then fsyncs dst's folder. It
// fails with an error matching fs.ErrExist if dst exists.
func Place(tmp, dst string) error {
	err := RenameNoReplace(tmp, dst)
	if fi, lerr := os.Lstat(tmp); IsCrossDevice(err) && lerr == nil && fi.IsDir() {
		// overlayfs refuses to rename a folder even within one folder.
		// shortcut: dst is visible while it fills and a crash leaves it
		// half-built; fine for overlay, the only place this runs.
		err = moveTree(tmp, dst)
	}
	if err != nil {
		return err
	}
	return SyncDir(filepath.Dir(dst))
}

// moveTree makes the folder dst and moves src's contents into it file by
// file, never replacing anything; on failure it removes the dst it made.
func moveTree(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dst, fi.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	for _, e := range entries {
		if err != nil {
			break
		}
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			err = moveTree(s, d)
		} else {
			err = RenameNoReplace(s, d)
		}
	}
	if err == nil {
		err = SyncDir(dst)
	}
	if err == nil {
		return os.Remove(src)
	}
	os.RemoveAll(dst)
	return err
}

// copyEntry creates dst fresh as a copy of src; made reports whether dst was created.
func copyEntry(src, dst string, made *bool) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
		*made = true
		return nil
	case fi.IsDir():
		if err := os.Mkdir(dst, 0o700); err != nil {
			return err
		}
		*made = true
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			var sub bool
			if err := copyEntry(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), &sub); err != nil {
				return err
			}
		}
		if err := os.Chmod(dst, fi.Mode().Perm()); err != nil {
			return err
		}
		return SyncDir(dst)
	case fi.Mode().IsRegular():
		return copyFile(src, dst, fi, made)
	}
	return fmt.Errorf("copy %s: not a regular file, folder or symlink", src)
}

func copyFile(src, dst string, fi fs.FileInfo, made *bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	*made = true
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(fi.Mode().Perm())
	}
	if err == nil {
		err = os.Chtimes(dst, fi.ModTime(), fi.ModTime()) // a move keeps mtime; so does its copy fallback
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// SyncDir fsyncs a directory, making the names in it durable.
func SyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
