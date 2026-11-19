package daemon

import (
	"context"
	"path/filepath"

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

// legacy means an unfinished job from before staging (decision 67): it
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
