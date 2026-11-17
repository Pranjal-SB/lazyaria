// Package trash moves files to the freedesktop.org trash (spec 1.0) and back.
// Only the home trash and the per-user $topdir/.Trash-$uid form are used; the
// shared $topdir/.Trash/$uid form is not implemented.
package trash

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrNoTrash means no usable trash exists on the file's device. Nothing was
// moved; ask the user before deleting for real.
var ErrNoTrash = errors.New("no usable trash directory on this filesystem")

// Entry describes one trashed item, enough to restore it.
type Entry struct {
	TrashedPath string
	InfoPath    string
	Original    string
}

func homeTrash() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "Trash")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "Trash")
}

// mountTop resolves symlinks in dir, then walks up while the device id stays the same.
func mountTop(dir string, dev uint64) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r // a symlinked parent must not hide the real mount root
	}
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		fi, err := os.Stat(parent)
		if err != nil {
			return dir
		}
		if d, _ := device(fi); d != dev {
			return dir
		}
		dir = parent
	}
}

func prepare(dir string) error {
	for _, sub := range []string{"files", "info"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// pick returns the trash dir for abs and the topdir ("" for the home trash).
func pick(abs string, dev uint64) (dir, top string, err error) {
	if h := homeTrash(); h != "" && prepare(h) == nil {
		if fi, e := os.Stat(h); e == nil {
			if d, _ := device(fi); d == dev {
				return h, "", nil
			}
		}
	}
	top = mountTop(filepath.Dir(abs), dev)
	if dir, err = topTrash(top); err != nil {
		return "", "", err
	}
	return dir, top, nil
}

// topTrash returns $topdir/.Trash-$uid, made if missing. One found there
// is used only as a real directory the user owns: on a shared drive
// another user may have made it first, or planted a symlink.
func topTrash(top string) (string, error) {
	dir := filepath.Join(top, ".Trash-"+strconv.Itoa(os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", ErrNoTrash
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() { // a symlink is not a directory to Lstat
		return "", ErrNoTrash
	}
	if uid, ok := owner(fi); !ok || uid != os.Getuid() {
		return "", ErrNoTrash
	}
	if err := prepare(dir); err != nil {
		return "", ErrNoTrash
	}
	return dir, nil
}

func encodePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// locate returns the absolute path with the parent's symlinks resolved (the
// last element is kept: it may itself be a symlink to trash), and its device.
func locate(path string) (string, uint64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", 0, err
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return "", 0, err
	}
	dev, ok := device(fi)
	if !ok {
		return "", 0, ErrNoTrash
	}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		abs = filepath.Join(parent, filepath.Base(abs))
	}
	return abs, dev, nil
}

// Check reports whether Trash(path) could find a trash directory. It moves
// nothing. A missing path returns the Lstat error.
func Check(path string) error {
	abs, dev, err := locate(path)
	if err != nil {
		return err
	}
	_, _, err = pick(abs, dev)
	return err
}

// Trash moves path into the trash. It never copies across devices.
func Trash(path string) (Entry, error) {
	abs, dev, err := locate(path)
	if err != nil {
		return Entry{}, err
	}
	dir, top, err := pick(abs, dev)
	if err != nil {
		return Entry{}, err
	}
	recorded := abs
	if top != "" {
		rel, err := filepath.Rel(top, abs)
		if err != nil {
			return Entry{}, err
		}
		if rel != ".." && !strings.HasPrefix(rel, "../") {
			recorded = rel
		}
	}
	body := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		encodePath(filepath.ToSlash(recorded)), time.Now().Format("2006-01-02T15:04:05"))

	base := filepath.Base(abs)
	for n := 1; n < 100000; n++ {
		name := base
		if n > 1 {
			name = base + "." + strconv.Itoa(n)
		}
		e := Entry{
			TrashedPath: filepath.Join(dir, "files", name),
			InfoPath:    filepath.Join(dir, "info", name+".trashinfo"),
			Original:    abs,
		}
		if _, err := os.Lstat(e.TrashedPath); err == nil {
			continue
		}
		f, err := os.OpenFile(e.InfoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return Entry{}, err
		}
		_, werr := f.WriteString(body)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = renameFn(abs, e.TrashedPath)
		}
		if werr != nil {
			os.Remove(e.InfoPath)
			if errors.Is(werr, os.ErrExist) {
				continue // the name was taken after the check; try the next
			}
			if errors.Is(werr, syscall.EXDEV) {
				return Entry{}, ErrNoTrash
			}
			return Entry{}, werr
		}
		return e, nil
	}
	return Entry{}, fmt.Errorf("trash: too many name collisions for %q", base)
}

// Restore puts a trashed item back and drops its .trashinfo. It refuses to
// overwrite anything already at the original path.
func Restore(e Entry) error {
	if _, err := os.Lstat(e.Original); err == nil {
		return fmt.Errorf("restore: %s already exists; move it aside and retry", e.Original)
	} else if !os.IsNotExist(err) {
		return err
	}
	beforeMove()
	if err := moveNoReplace(e.TrashedPath, e.Original); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("restore: %s already exists; move it aside and retry", e.Original)
		}
		return err
	}
	if err := os.Remove(e.InfoPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
