package daemon

import (
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/xattr"
)

// stampOrigin records the first link (and referrer) on a finished job's
// files when privacy.stamp_origin_xattr is on. Failures are only logged.
func (m *Manager) stampOrigin(j *store.Job) {
	if !m.s.Privacy.StampOriginXattr || len(j.URIs) == 0 {
		return
	}
	ref := ""
	if j.Referrer != nil {
		ref = *j.Referrer
	}
	for _, p := range payloadFiles(j) {
		if err := xattr.StampOrigin(p, j.URIs[0], ref); err != nil {
			m.log.Warn("stamp origin", "job", j.ID, "path", p, "err", err)
		}
	}
}
