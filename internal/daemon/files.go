package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// FileRemover takes a removed job's files out of the download directory and
// can bring them back for Undo.
type FileRemover interface {
	RemoveFiles(ctx context.Context, j *store.Job) ([]trash.Entry, error)
	RestoreFiles(ctx context.Context, es []trash.Entry) error
}

// NeedsConfirmError means the files cannot go to a trash. Nothing was
// deleted and the row is unchanged; deleting for real needs the user's say-so.
type NeedsConfirmError struct{ Path string }

func (e *NeedsConfirmError) Error() string {
	return fmt.Sprintf("no trash is available for %s; nothing was deleted. Remove the job without deleting files, or confirm a permanent delete", e.Path)
}

// TrashFiles is the default FileRemover: it moves the payload and its
// .aria2 control file to the freedesktop trash and never deletes anything.
type TrashFiles struct {
	trash func(string) (trash.Entry, error) // tests inject failures
}

func (t *TrashFiles) move(p string) (trash.Entry, error) {
	if t.trash != nil {
		return t.trash(p)
	}
	return trash.Trash(p)
}

func (t *TrashFiles) RemoveFiles(_ context.Context, j *store.Job) ([]trash.Entry, error) {
	p, ok := payloadPath(j)
	if !ok {
		return nil, nil
	}
	var done []trash.Entry
	for _, f := range []string{p, p + ".aria2"} {
		e, err := t.move(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			rerr := t.RestoreFiles(context.Background(), done)
			if errors.Is(err, trash.ErrNoTrash) {
				err = &NeedsConfirmError{Path: f}
			}
			return nil, errors.Join(err, rerr)
		}
		done = append(done, e)
	}
	return done, nil
}

func (t *TrashFiles) RestoreFiles(_ context.Context, es []trash.Entry) error {
	var errs []error
	for _, e := range es {
		if err := trash.Restore(e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// payloadPath is where a job's download lands: dir/out, or dir/name for a
// torrent's file or top-level directory. It refuses anything outside dir.
func payloadPath(j *store.Job) (string, bool) {
	name := j.Name
	if j.Out != nil && *j.Out != "" {
		name = *j.Out
	}
	if j.Dir == "" || name == "" || !filepath.IsLocal(name) {
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
