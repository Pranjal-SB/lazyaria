package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// stagingDir is where a staged job's download runs. It is built from the
// job's own Dir, never from the current config.
func stagingDir(j *store.Job) string { return filepath.Join(j.Dir, ".laria", j.ID) }

// workDir is the folder j's Out and Name are relative to: the staging folder
// while the job is staged, else its download folder.
func workDir(j *store.Job) string {
	if j.Staged {
		return stagingDir(j)
	}
	return j.Dir
}

// resumable: aria2 can resume j's staged file from its control file.
func resumable(j *store.Job) bool {
	return j.Out != nil && exists(filepath.Join(stagingDir(j), *j.Out)+".aria2")
}

// controlledPartial finds the file a staged download wrote before its name
// reached the row (a crash before the first poll): the only file in the
// staging folder with a control file beside it. A link to a metalink or a
// .torrent writes other files than its own (and the row holds the
// "download" placeholder until the first poll), so those never count.
func controlledPartial(j *store.Job) (string, bool) {
	if j.Name == "download" {
		return "", false
	}
	es, _ := os.ReadDir(stagingDir(j))
	var found []string
	for _, e := range es {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".meta4", ".metalink", ".torrent":
			return "", false
		}
		if name, ok := strings.CutSuffix(e.Name(), ".aria2"); ok && name != "" && exists(filepath.Join(stagingDir(j), name)) {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// emptyStaging removes everything in j's staging folder, which the job owns
// by construction. Without a control file aria2 cannot resume, and with
// continue=true it would take a stale file for finished.
func emptyStaging(j *store.Job) error {
	dir := stagingDir(j)
	es, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range es {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// stagingRefs lists the engine downloads other than j's own that write into
// j's staging folder (a metalink's further files) and are not finished.
// ok is false when the engine cannot say.
func (m *Manager) stagingRefs(ctx context.Context, j *store.Job) (refs []engine.EngineRef, ok bool) {
	list, err := m.e.List(ctx)
	if err != nil {
		return nil, false
	}
	prefix := stagingDir(j) + string(filepath.Separator)
	for _, st := range list {
		if (j.EngineRef != nil && st.Ref == refOf(j)) || !strings.HasPrefix(st.Path, prefix) {
			continue
		}
		if st.State == "active" || st.State == "waiting" || st.State == "paused" {
			refs = append(refs, st.Ref)
		}
	}
	return refs, true
}

// legacy means an unfinished job from a version before staging folders: it
// downloads straight into Dir and is stopped, never resumed.
func legacy(j *store.Job) bool { return !j.Staged && j.State != store.Seeding }

// stopLegacy stops an unfinished pre-staging job with an error. Its files
// are left exactly as they are.
func (m *Manager) stopLegacy(ctx context.Context, j *store.Job) {
	if j.EngineRef != nil {
		if err := m.e.Remove(ctx, refOf(j)); err != nil {
			m.log.Warn("stop legacy download", "job", j.ID, "err", err)
		}
	}
	m.cancelRetry(j.ID)
	kind, msg := "legacy", legacyMsg(j)
	m.to(ctx, j, store.Error, kind, func(x *store.Job) { x.ErrorKind, x.ErrorCode, x.ErrorMsg, x.RetryAt = &kind, nil, &msg, nil })
}

func legacyMsg(j *store.Job) string {
	return "started by an older lazyaria; its file is untouched in " + j.Dir + "; add the link again to download it with staging"
}
