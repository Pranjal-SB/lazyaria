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
	// trashed, without moving anything. files lists what the download
	// writes inside a payload folder (see targets).
	Check(ctx context.Context, j *store.Job, files []string) error
	RemoveFiles(ctx context.Context, j *store.Job, files []string) ([]trash.Entry, error)
	RestoreFiles(ctx context.Context, es []trash.Entry) error
}

// NeedsConfirmError means the files cannot go to a trash. lazyaria never
// deletes for real, so nothing was deleted and the job was kept; one that
// was running comes back paused. The user removes the job without its
// files and deals with them by hand.
type NeedsConfirmError struct{ Path string }

func (e *NeedsConfirmError) Error() string {
	return fmt.Sprintf("no trash is available for %s; the files are untouched and the job was kept (paused, if it was running). Remove the job without --delete-files, then move or delete the files yourself", e.Path)
}

// TrashFiles is the default FileRemover: it moves what targets lists (a
// staged job's staging folder, a finished job's payload) to the
// freedesktop trash and never deletes anything.
type TrashFiles struct {
	trash func(string) (trash.Entry, error) // tests inject failures
	check func(string) error
}

// move trashes p, listed in the trash as shownAs when that is set.
func (t *TrashFiles) move(p, shownAs string) (trash.Entry, error) {
	if t.trash != nil {
		return t.trash(p)
	}
	return trash.TrashAs(p, shownAs)
}

// unfinishedName is how an unfinished download's staging folder shows in
// the trash: "<name> (unfinished)" beside where the download would have
// landed, instead of a folder named by the job's id.
func unfinishedName(j *store.Job) string {
	name := j.ID
	if p, ok := payloadPath(&store.Job{Dir: j.Dir, Name: j.Name, Out: j.Out}); ok {
		name = filepath.Base(p)
	}
	return filepath.Join(j.Dir, fmt.Sprintf("%s (unfinished)", name))
}

func (t *TrashFiles) probe(p string) error {
	if t.check != nil {
		return t.check(p)
	}
	return trash.Check(p)
}

// targets lists what removing j may trash. A staged job owns its whole
// staging folder, plus what a recorded publish already moved into Dir
// (matched by inode). A finished job owns its payload in Dir; a payload
// folder may predate the job and hold other files, so only the listed
// files inside it are taken, never the folder itself. Anything else, such
// as an unfinished job from before staging, owns nothing.
func targets(j *store.Job, files []string) []string {
	if j.Staged {
		var out []string
		if j.Publish != nil {
			for _, e := range j.Publish.Entries {
				if p := filepath.Join(j.Dir, e.Name); sameFile(p, e.Ino, e.Dev, e.Size) {
					out = append(out, p)
				}
			}
		}
		// A seeding torrent's staging copy is removed, not trashed: it is
		// the published copy's second name.
		if lexists(stagingDir(j)) && !seedsFromStaging(j) {
			out = append(out, stagingDir(j))
		}
		return out
	}
	if j.Publish != nil {
		return publishedFiles(j, files)
	}
	p, ok := payloadPath(j)
	if !ok || (j.State != store.Done && j.State != store.Seeding) {
		return nil
	}
	return inPayload(p, files)
}

// publishedFiles lists what a finished job put in Dir, from its publish
// record: each entry still there as published, and for a folder entry only
// the listed files inside it (it may hold the user's files by now).
func publishedFiles(j *store.Job, files []string) []string {
	var out []string
	for _, p := range published(j) {
		if isDir(p) {
			out = append(out, inPayload(p, files)...)
		} else {
			out = append(out, p)
		}
	}
	return out
}

func lexists(p string) bool { _, err := os.Lstat(p); return err == nil }

func isDir(p string) bool { fi, err := os.Lstat(p); return err == nil && fi.IsDir() }

func (t *TrashFiles) Check(_ context.Context, j *store.Job, files []string) error {
	for _, f := range targets(j, files) {
		if err := t.probe(f); errors.Is(err, trash.ErrNoTrash) {
			return &NeedsConfirmError{Path: f}
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (t *TrashFiles) RemoveFiles(_ context.Context, j *store.Job, files []string) ([]trash.Entry, error) {
	var done []trash.Entry
	for _, f := range targets(j, files) {
		shown := ""
		if j.Staged && f == stagingDir(j) {
			shown = unfinishedName(j)
		}
		e, err := t.move(f, shown)
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
	if p, ok := payloadPath(j); ok && !j.Staged {
		// The trash records resolved paths; match them.
		if real, err := filepath.EvalSymlinks(p); err == nil {
			pruneEmpty(real, done)
		}
	}
	if j.Staged {
		os.Remove(filepath.Dir(stagingDir(j))) // .laria, once no job uses it
	}
	return done, nil
}

// pruneEmpty removes the folders below root that the trashed files left
// empty. root itself stays: it may be the user's own folder. os.Remove
// refuses a folder that is not empty.
func pruneEmpty(root string, es []trash.Entry) {
	for _, e := range es {
		for d := filepath.Dir(e.Original); strings.HasPrefix(d, root+string(filepath.Separator)); d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
	}
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
		// The folder may have been pruned when it was left empty.
		if err := os.MkdirAll(filepath.Dir(e.Original), 0o755); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := trash.Restore(e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// payloadPath is where a job's download lands, under workDir: dir/out, or dir/name for a
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
	return filepath.Join(workDir(j), name), true
}

// inPayload lists the payload p when it is a file, else those of files
// that exist inside the folder p. A folder may predate the job and hold
// other files, so it never counts as a whole.
func inPayload(p string, files []string) []string {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil
	}
	if !fi.IsDir() {
		return []string{p}
	}
	var out []string
	for _, f := range files {
		if rel, err := filepath.Rel(p, f); err == nil && filepath.IsLocal(rel) && lexists(f) {
			out = append(out, f)
		}
	}
	return out
}
