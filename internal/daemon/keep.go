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

// keepRename moves kept files and keepTrash trashes their control files;
// tests watch the order through them.
var (
	keepRename = fsutil.RenameNoReplace
	keepTrash  = trash.TrashAs
)

// keptFile is a file a remove moved out of a staging folder, for Undo.
// Ino and Dev identify it at To, so Undo never takes another file.
type keptFile struct {
	From, To string
	Ino, Dev uint64
}

// keepUnfinished moves a removed unfinished download out of its staging
// folder into Dir, so the files the remove keeps are where the user looks:
// each payload entry as "<stem> (unfinished)<ext>" of the name it would be
// published under (the next free one if taken). Its .aria2 control file,
// which only an Undo needs, goes to the trash, listed as the kept name plus
// .aria2; without a usable trash it moves beside the payload instead.
// clearStaging then removes the folder. If a step fails, the ones done are
// undone and the files stay in the staging folder. It returns the moved
// files and the trashed control files, for Undo.
func keepUnfinished(j *store.Job) ([]keptFile, []trash.Entry, error) {
	staging := stagingDir(j)
	if !j.Staged || j.Publish != nil || !lexists(staging) {
		return nil, nil, nil
	}
	entries, err := publishEntries(staging)
	if err != nil {
		return nil, nil, err
	}
	var done []keptFile
	var trashed []trash.Entry
	fail := func(err error) ([]keptFile, []trash.Entry, error) {
		return nil, nil, errors.Join(err, putBack(done), restoreAll(trashed))
	}
	held := map[string]bool{}
	for _, e := range entries {
		name, err := finalName(j, unfinishedBase(chosenName(j, e.Rel)), held)
		if err == nil && lexists(filepath.Join(j.Dir, name+".aria2")) {
			name, err = freeName(filepath.Join(j.Dir, name), held)
		}
		if err != nil {
			return fail(err)
		}
		held[name] = true
		// The control file goes first: a crash in between leaves the payload
		// in staging, never alone under a name. A partial without one (a
		// reply of unknown length) still moves: the name says unfinished.
		payload := keptFile{From: filepath.Join(staging, e.Rel), To: filepath.Join(j.Dir, name)}
		var pairs []keptFile
		if ctl := payload.From + ".aria2"; lexists(ctl) {
			te, err := keepTrash(ctl, payload.To+".aria2")
			switch {
			case err == nil:
				trashed = append(trashed, te)
			case errors.Is(err, trash.ErrNoTrash):
				pairs = append(pairs, keptFile{From: ctl, To: payload.To + ".aria2"})
			default:
				return fail(err)
			}
		}
		pairs = append(pairs, payload)
		for _, k := range pairs {
			if err := keepRename(k.From, k.To); err != nil {
				return fail(err)
			}
			// A rename keeps the inode; the move is undone at once if
			// the file cannot be identified afterwards.
			var err error
			if k.Ino, k.Dev, _, err = fsutil.FileID(k.To); err != nil {
				keepRename(k.To, k.From)
				return fail(err)
			}
			done = append(done, k)
		}
	}
	return done, trashed, nil
}

// restoreAll puts trashed files back, last first.
func restoreAll(es []trash.Entry) error {
	var errs []error
	for i := len(es) - 1; i >= 0; i-- {
		errs = append(errs, trash.Restore(es[i]))
	}
	return errors.Join(errs...)
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
// no longer at its kept name (put back already, or the user took it or
// put another file there) is skipped. It first checks every staging path
// is free, so a blocked one fails the whole set before anything moves.
func putBack(ks []keptFile) error {
	ks = slices.DeleteFunc(slices.Clone(ks), func(k keptFile) bool { return !sameFile(k.To, k.Ino, k.Dev, -1) })
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
