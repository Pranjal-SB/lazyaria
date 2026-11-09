package daemon

import (
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/xattr"
)

// stampOrigin records the first link (and referrer) on a finished job's
// files when privacy.stamp_origin_xattr is on. Failures are only logged, at
// debug: filesystems without xattrs are ignored by xattr itself.
// shortcut: a torrent added from a file has no link, so it is not stamped
// (spec §8.6 "comment URL" deferred); a magnet's URI is stamped.
func (m *Manager) stampOrigin(j *store.Job) {
	if !m.s.Load().Privacy.StampOriginXattr || len(j.URIs) == 0 {
		return
	}
	ref := ""
	if j.Referrer != nil {
		ref = *j.Referrer
	}
	for _, p := range payloadFiles(j) {
		if err := xattr.StampOrigin(p, j.URIs[0], ref); err != nil {
			m.log.Debug("stamp origin", "job", j.ID, "path", p, "err", err)
		}
	}
}
