package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/Pranjal-SB/lazyaria/internal/fsutil"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/trash"
)

// keepRename moves kept files; tests watch the order through it.
var keepRename = fsutil.RenameNoReplace

// keptFile is a file a remove moved out of a staging folder, for Undo.
type keptFile struct{ From, To string }

// keepUnfinished moves a removed unfinished download out of its staging
// folder into Dir, so the files the remove keeps are where the user looks:
// each payload entry as "<stem> (unfinished)<ext>" of the name it would be
// published under (the next free one if taken), with its .aria2 control
// file beside it. clearStaging then removes the folder. If a move fails,
// the ones done are undone and the files stay in the staging folder.
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
		name, err := finalName(j, unfinishedBase(chosenName(j, e.Rel)), held)
		if err == nil && lexists(filepath.Join(j.Dir, name+".aria2")) {
			name, err = freeName(filepath.Join(j.Dir, name), held)
		}
		if err != nil {
			return nil, errors.Join(err, putBack(done))
		}
		held[name] = true
		// The control file goes first: a crash in between leaves the payload
		// in staging, never alone under a name. A partial without one (a
		// reply of unknown length) still moves: the name says unfinished.
		payload := keptFile{filepath.Join(staging, e.Rel), filepath.Join(j.Dir, name)}
		var pairs []keptFile
		if ctl := payload.From + ".aria2"; lexists(ctl) {
			pairs = append(pairs, keptFile{ctl, payload.To + ".aria2"})
		}
		pairs = append(pairs, payload)
		for _, k := range pairs {
			if err := keepRename(k.From, k.To); err != nil {
				return nil, errors.Join(err, putBack(done))
			}
			done = append(done, k)
		}
	}
	return done, nil
}

// infoHashTorrent is the metadata aria2 saves for a magnet link,
// <info-hash>.torrent; a re-add fetches it again.
var infoHashTorrent = regexp.MustCompile(`^[0-9a-fA-F]{40}\.torrent$`)

// clearStaging removes j's staging folder once keepUnfinished has moved the
// payload out. Only aria2's saved magnet metadata is deleted; anything else
// left, which may be the user's bytes, goes to the trash in the folder, and
// its entry is returned for Undo.
func (m *Manager) clearStaging(j *store.Job) ([]trash.Entry, error) {
	staging := stagingDir(j)
	es, _ := os.ReadDir(staging)
	for _, e := range es {
		if infoHashTorrent.MatchString(e.Name()) {
			os.Remove(filepath.Join(staging, e.Name()))
		}
	}
	if err := os.Remove(staging); err != nil && lexists(staging) {
		te, err := m.trash(staging)
		if err != nil {
			return nil, err
		}
		os.Remove(filepath.Dir(staging))
		return []trash.Entry{te}, nil
	}
	os.Remove(filepath.Dir(staging)) // .laria, once no job uses it
	return nil, nil
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
