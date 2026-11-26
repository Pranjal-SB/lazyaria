package daemon

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/Pranjal-SB/lazyaria/internal/fsutil"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// publishLinks publishes a finished torrent that aria2 goes on seeding,
// without waiting for the seeding to end: its files are hard-linked into
// .up/ in staging and that tree is renamed into Dir, so all of it appears
// at once while aria2 keeps reading the staging copy. The rename follows publish's recorded intent.
// Where links fail (FAT, exFAT), reseed takes over.
func (m *Manager) publishLinks(ctx context.Context, j *store.Job) (*store.Job, error) {
	staging := stagingDir(j)
	entries, err := publishEntries(staging)
	if j.Publish != nil || err != nil || len(entries) == 0 {
		return m.publish(ctx, j) // resumes the intent, or records the failure
	}
	up := filepath.Join(staging, upDir)
	os.RemoveAll(up) // a tree a crash left half built
	held := map[string]bool{}
	for i := range entries {
		e := &entries[i]
		rel := filepath.Join(upDir, e.Rel)
		if err := linkTree(filepath.Join(staging, e.Rel), filepath.Join(staging, rel)); err != nil {
			os.RemoveAll(up)
			if fsutil.LinkUnsupported(err) {
				return m.reseed(ctx, j)
			}
			return m.publishFailed(ctx, j, err)
		}
		if e.Name, err = finalName(j, e.Rel, held); err != nil {
			return m.publishFailed(ctx, j, err)
		}
		held[e.Name] = true
		e.Rel = rel
		if e.Ino, e.Dev, e.Size, err = fsutil.FileID(filepath.Join(staging, rel)); err != nil {
			return m.publishFailed(ctx, j, err)
		}
	}
	if j, err = m.record(ctx, j, &store.Publish{Name: entries[0].Name, Entries: entries, Seed: true}); err != nil {
		return m.publishFailed(ctx, j, err)
	}
	return m.publish(ctx, j)
}

// linkTree hard-links the file src, or every file of the folder src, at dst.
func linkTree(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if !d.IsDir() {
			return publishLink(p, to)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return os.Mkdir(to, fi.Mode().Perm())
	})
}

// reseed publishes a torrent where hard links fail: aria2 lets go of it,
// the files move up as any download's do, and aria2 seeds them from Dir.
// The hash check is skipped only when every file is still the one aria2
// wrote: bt-seed-unverified would seed a damaged file as it is.
func (m *Manager) reseed(ctx context.Context, j *store.Job) (*store.Job, error) {
	ids, err := payloadIDs(stagingDir(j))
	if err != nil {
		return m.publishFailed(ctx, j, err)
	}
	if err := m.e.Remove(ctx, refOf(j)); err != nil {
		return j, err // still seeding from staging; the next poll tries again
	}
	if j, err = m.publish(ctx, j); err != nil || j.State != store.Done {
		return j, err
	}
	// shortcut: seeding goes on only under the torrent's own name and with
	// its .torrent stored; a renamed copy or a magnet ends done instead.
	// Seeding those needs aria2's index-out per file, or the metadata kept.
	tor := m.torrent(ctx, j)
	if e := j.Publish.Entries; len(e) != 1 || e[0].Name != e[0].Rel || len(tor) == 0 {
		m.log.Info("published without seeding: this folder cannot hold links and the torrent cannot be seeded from its new name", "job", j.ID)
		return j, nil
	}
	from := "unverified"
	for rel, id := range ids {
		if !sameFile(filepath.Join(j.Dir, rel), id.Ino, id.Dev, id.Size) {
			from = "check"
		}
	}
	req := m.request(j, false)
	req.Torrent, req.Out, req.SeedFrom = tor, "", from
	ref, err := m.e.Add(ctx, req)
	if err != nil {
		m.log.Warn("seed the published torrent again", "job", j.ID, "err", err)
		return j, nil
	}
	return m.change(ctx, j.ID, "reseeded", from, func(x *store.Job) {
		r := string(ref)
		x.EngineRef, x.State = &r, store.Seeding
	})
}

// payloadIDs records the identity of every file of the payload in staging,
// by path relative to it.
func payloadIDs(staging string) (map[string]store.PublishEntry, error) {
	es, err := publishEntries(staging)
	if err != nil {
		return nil, err
	}
	ids := map[string]store.PublishEntry{}
	for _, e := range es {
		err := filepath.WalkDir(filepath.Join(staging, e.Rel), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(staging, p)
			if err != nil {
				return err
			}
			var id store.PublishEntry
			id.Ino, id.Dev, id.Size, err = fsutil.FileID(p)
			ids[rel] = id
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// seedsFromStaging: j was published by links and aria2 seeds it from the
// staging copy.
func seedsFromStaging(j *store.Job) bool {
	return j.Staged && j.State == store.Seeding && j.Publish != nil && j.Publish.Seed
}

// anyPublished: something j published is still in Dir as it put it there.
func anyPublished(j *store.Job) bool {
	return slices.ContainsFunc(j.Publish.Entries, func(e store.PublishEntry) bool {
		return sameFile(filepath.Join(j.Dir, e.Name), e.Ino, e.Dev, -1)
	})
}

// endSeeding marks a torrent done once aria2 no longer seeds it, and drops
// the staging copy it seeded from: the files are in Dir as hard links of
// it, so nothing the user still has there goes.
func (m *Manager) endSeeding(ctx context.Context, j *store.Job, why string) *store.Job {
	staged := seedsFromStaging(j)
	if staged {
		m.dropStaging(j)
	}
	now := m.now()
	return m.to(ctx, j, store.Done, why, func(x *store.Job) {
		x.FinishedAt = &now
		if staged {
			x.Staged = false
		}
	})
}

// dropStaging removes j's staging folder, which the job owns by
// construction, and .laria once no job uses it.
func (m *Manager) dropStaging(j *store.Job) {
	if err := os.RemoveAll(stagingDir(j)); err != nil {
		m.log.Warn("remove the staging folder", "job", j.ID, "path", stagingDir(j), "err", err)
	}
	os.Remove(filepath.Dir(stagingDir(j)))
}
