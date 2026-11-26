package daemon

import (
	"context"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/xattr"
)

// stampOrigin records the first link (and referrer) on a finished job's
// files when privacy.stamp_origin_xattr is on. Failures are only logged, at
// debug: filesystems without xattrs are ignored by xattr itself.
// shortcut: a torrent added from a file has no link, so it is not stamped
// (using the torrent's comment URL is left for later); a magnet's URI is
// stamped.
func (m *Manager) stampOrigin(ctx context.Context, j *store.Job) {
	p, ok := payloadPath(j)
	if !m.s.Load().Privacy.StampOriginXattr || len(j.URIs) == 0 || !ok {
		return
	}
	ref := ""
	if j.Referrer != nil {
		ref = *j.Referrer
	}
	// Only the job's own files: its folder may hold the user's too.
	fs := inPayload(p, m.downloadFiles(ctx, j))
	if j.Publish != nil && !j.Staged {
		fs = publishedFiles(j, m.downloadFiles(ctx, j))
	}
	for _, f := range fs {
		if err := xattr.StampOrigin(f, j.URIs[0], ref); err != nil {
			m.log.Debug("stamp origin", "job", j.ID, "path", f, "err", err)
		}
	}
}
