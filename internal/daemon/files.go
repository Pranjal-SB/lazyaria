package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// FileRemover takes a removed job's files out of the download directory and
// can bring them back for Undo.
type FileRemover interface {
	// Check reports (as *NeedsConfirmError) whether the files could be
	// trashed, without moving anything.
	Check(ctx context.Context, j *store.Job) error
	RemoveFiles(ctx context.Context, j *store.Job) ([]trash.Entry, error)
	RestoreFiles(ctx context.Context, es []trash.Entry) error
}

// NeedsConfirmError means the files cannot go to a trash. Nothing was
// deleted and the job is as it was; deleting for real needs the user's say-so.
type NeedsConfirmError struct{ Path string }

func (e *NeedsConfirmError) Error() string {
	return fmt.Sprintf("no trash is available for %s; the files are untouched and the job is as it was. Remove the job without deleting files, then move or delete them yourself", e.Path)
}

// TrashFiles is the default FileRemover: it moves the payload and its
// .aria2 control file to the freedesktop trash and never deletes anything.
type TrashFiles struct {
	trash func(string) (trash.Entry, error) // tests inject failures
	check func(string) error
}

func (t *TrashFiles) move(p string) (trash.Entry, error) {
	if t.trash != nil {
		return t.trash(p)
	}
	return trash.Trash(p)
}

func (t *TrashFiles) probe(p string) error {
	if t.check != nil {
		return t.check(p)
	}
	return trash.Check(p)
}

// targets lists what removing j may trash. The payload counts only when the
// job provably owns it: it wrote bytes there, or finished. So a duplicate
// "download anyway", a job that failed before its first byte, or a magnet
// still named "download" never takes a file that was already there.
func targets(j *store.Job) []string {
	p, ok := payloadPath(j)
	if !ok {
		return nil
	}
	var out []string
	owned := (j.DoneBytes != nil && *j.DoneBytes > 0) || j.State == store.Done || j.State == store.Seeding
	if owned && lexists(p) {
		out = append(out, p)
	}
	if lexists(p + ".aria2") {
		out = append(out, p+".aria2")
	}
	return out
}

func lexists(p string) bool { _, err := os.Lstat(p); return err == nil }

func (t *TrashFiles) Check(_ context.Context, j *store.Job) error {
	for _, f := range targets(j) {
		if err := t.probe(f); errors.Is(err, trash.ErrNoTrash) {
			return &NeedsConfirmError{Path: f}
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (t *TrashFiles) RemoveFiles(_ context.Context, j *store.Job) ([]trash.Entry, error) {
	var done []trash.Entry
	for _, f := range targets(j) {
		e, err := t.move(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if rerr := t.RestoreFiles(context.Background(), done); rerr != nil {
				var where []string
				for _, d := range done {
					where = append(where, d.TrashedPath)
				}
				return nil, fmt.Errorf("trashing %s failed (%v) and putting back the files already trashed failed too; they are in the trash at %s: %w",
					f, err, strings.Join(where, ", "), rerr)
			}
			if errors.Is(err, trash.ErrNoTrash) {
				err = &NeedsConfirmError{Path: f}
			}
			return nil, err
		}
		done = append(done, e)
	}
	return done, nil
}

// RestoreFiles puts es back. It first checks that every original path is
// free, so a blocked entry fails the whole set before anything moves.
func (t *TrashFiles) RestoreFiles(_ context.Context, es []trash.Entry) error {
	for _, e := range es {
		if lexists(e.Original) {
			return fmt.Errorf("%s already exists; move it aside and retry (the trashed copy is %s)", e.Original, e.TrashedPath)
		}
	}
	var errs []error
	for _, e := range es {
		if err := trash.Restore(e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// payloadPath is where a job's download lands: dir/out, or dir/name for a
// torrent's file or top-level directory. It refuses anything outside dir,
// dir itself, and the placeholder name of a torrent whose metadata is missing.
func payloadPath(j *store.Job) (string, bool) {
	name := j.Name
	if j.Out != nil && *j.Out != "" {
		name = *j.Out
	} else if name == "download" {
		return "", false
	}
	if j.Dir == "" || name == "" || filepath.Clean(name) == "." || !filepath.IsLocal(name) {
		return "", false
	}
	return filepath.Join(j.Dir, name), true
}

// payloadFiles lists the regular files of a job's payload.
func payloadFiles(j *store.Job) []string {
	p, ok := payloadPath(j)
	if !ok {
		return nil
	}
	var out []string
	filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && !strings.HasSuffix(path, ".aria2") {
			out = append(out, path)
		}
		return nil
	})
	return out
}
