package daemon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/fsutil"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// The marker shows an unfinished download where people look for it: a
// relative symbolic link <name>.laria in the download folder pointing at
// .laria/<id>/<out>, the file aria2 writes.

const markerExt = ".laria"

var markSymlink = os.Symlink // tests inject EPERM

// markerPath is j's marker in Dir, named like the file publish will put
// there, and where it points. ok is false when j gets no marker: a torrent,
// a file in a subfolder (a metalink's), or no name yet.
func markerPath(j *store.Job) (path, target string, ok bool) {
	if !j.Staged || isBT(j.Kind) || j.Out == nil || !filepath.IsLocal(*j.Out) || strings.ContainsRune(*j.Out, filepath.Separator) {
		return "", "", false
	}
	name := textutil.SanitizeName(chosenName(j, *j.Out), false)
	if name == "" {
		return "", "", false
	}
	return filepath.Join(j.Dir, name+markerExt), filepath.Join(".laria", j.ID, *j.Out), true
}

// mark creates or re-points j's marker while it downloads. It never
// replaces anything at that path but a marker of j's own, and a
// filesystem without links (FAT, exFAT) just gets none.
func (m *Manager) mark(j *store.Job) {
	if !m.s.Load().Downloads.Marker || j.Publish != nil {
		unmark(j) // the setting is live
		return
	}
	p, target, ok := markerPath(j)
	if !ok {
		return
	}
	cur, err := os.Readlink(p)
	switch {
	case err == nil && cur == target:
		return
	case err == nil && ownMarker(cur, j.ID):
		os.Remove(p)
	case !errors.Is(err, fs.ErrNotExist):
		return // a file or link that is not ours
	}
	if err := markSymlink(target, p); err != nil {
		if fsutil.LinkUnsupported(err) || errors.Is(err, fs.ErrExist) {
			m.log.Debug("no marker for the download", "job", j.ID, "path", p, "err", err)
		} else {
			m.log.Warn("create the download's marker", "job", j.ID, "path", p, "err", err)
		}
	}
}

// unmark removes j's marker, and only if it is j's own link.
func unmark(j *store.Job) {
	p, _, ok := markerPath(j)
	if !ok {
		return
	}
	if cur, err := os.Readlink(p); err == nil && ownMarker(cur, j.ID) {
		os.Remove(p)
	}
}

// ownMarker: target is a marker's link into the staging folder of job id,
// or of any job when id is "".
func ownMarker(target, id string) bool {
	parts := strings.Split(target, string(filepath.Separator))
	return len(parts) == 3 && parts[0] == ".laria" && parts[1] != "" && (id == "" || parts[1] == id) &&
		filepath.IsLocal(parts[2])
}

// dropDanglingMarkers removes the markers in dir whose file is gone: a
// crash, or a job whose staging folder went, left them.
func (m *Manager) dropDanglingMarkers(dir string) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if e.Type()&fs.ModeSymlink == 0 || !strings.HasSuffix(e.Name(), markerExt) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		cur, err := os.Readlink(p)
		if err != nil || !ownMarker(cur, "") {
			continue
		}
		if _, err := os.Stat(p); errors.Is(err, fs.ErrNotExist) {
			os.Remove(p)
		}
	}
}
