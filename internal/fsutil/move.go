package fsutil

import (
	"errors"
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
	err := RenameNoReplace(tmp, dst)
	if fi, lerr := os.Lstat(tmp); IsCrossDevice(err) && lerr == nil && fi.IsDir() {
		err = MoveInto(tmp, dst, false) // overlayfs refuses to rename a folder
	}
	if err != nil {
		os.RemoveAll(tmp) // the hidden temp only; a half-built dst stays
		return err
	}
	return SyncDir(filepath.Dir(dst))
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

// MoveInto builds the folder dst from the folder src by moving src's
// contents in one by one, never replacing anything, then removes the
// emptied src. It is for overlayfs, which refuses to rename a folder (EXDEV)
// even within one folder. dst is visible while it fills. Fresh, dst must not
// exist; with resume, an existing dst (an earlier, interrupted MoveInto) is
// filled further. On failure dst is left as it is: anything already in it
// may have been opened or added to.
func MoveInto(src, dst string, resume bool) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dst, fi.Mode().Perm()); err != nil && !(resume && errors.Is(err, fs.ErrExist) && isDir(dst)) {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			err = MoveInto(s, d, resume)
		} else {
			err = RenameNoReplace(s, d)
		}
		if err != nil {
			return err
		}
	}
	if err := SyncDir(dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func isDir(p string) bool { fi, err := os.Lstat(p); return err == nil && fi.IsDir() }

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
