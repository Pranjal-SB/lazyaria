package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/Pranjal-SB/lazyaria/internal/fsutil"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// keptFile is a file a remove moved out of a staging folder, for Undo.
type keptFile struct{ From, To string }

// keepUnfinished moves a removed unfinished download out of its staging
// folder into Dir, so the files the remove keeps are where the user looks:
// each payload entry under the name it would be published under (the next
// free one if taken), with its .aria2 control file beside it. The staging
// folder then goes. If a move fails, the ones done are undone and the files
// stay in the staging folder.
func keepUnfinished(j *store.Job) ([]keptFile, error) {
	staging := stagingDir(j)
	if !j.Staged || j.Publish != nil || !lexists(staging) {
		return nil, nil
	}
	entries, err := publishEntries(staging)
	if err != nil {
		return nil, err
	}
	var done []keptFile
	held := map[string]bool{}
	for _, e := range entries {
		name, err := finalName(j, chosenName(j, e.Rel), held)
		if err == nil && lexists(filepath.Join(j.Dir, name+".aria2")) {
			name, err = freeName(filepath.Join(j.Dir, name), held)
		}
		if err != nil {
			return nil, errors.Join(err, putBack(done))
		}
		held[name] = true
		pairs := []keptFile{{filepath.Join(staging, e.Rel), filepath.Join(j.Dir, name)}}
		if ctl := pairs[0].From + ".aria2"; lexists(ctl) {
			pairs = append(pairs, keptFile{ctl, pairs[0].To + ".aria2"})
		}
		for _, k := range pairs {
			if err := fsutil.RenameNoReplace(k.From, k.To); err != nil {
				return nil, errors.Join(err, putBack(done))
			}
			done = append(done, k)
		}
	}
	// What is left is aria2's own (.torrent, .meta4): a re-add fetches it again.
	if err := os.RemoveAll(staging); err != nil {
		return done, err
	}
	os.Remove(filepath.Dir(staging)) // .laria, once no job uses it
	return done, nil
}

// putBack returns kept files to their staging folder, last first. A file
// no longer at its kept name (put back already, or the user took it) is
// skipped. It first checks every staging path is free, so a blocked one
// fails the whole set before anything moves.
func putBack(ks []keptFile) error {
	ks = slices.DeleteFunc(slices.Clone(ks), func(k keptFile) bool { return !lexists(k.To) })
	for _, k := range ks {
		if lexists(k.From) {
			return fmt.Errorf("%s already exists; move it aside and retry (the kept file is %s)", k.From, k.To)
		}
	}
	var errs []error
	for i := len(ks) - 1; i >= 0; i-- {
		k := ks[i]
		if err := os.MkdirAll(filepath.Dir(k.From), 0o755); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := fsutil.RenameNoReplace(k.To, k.From); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
