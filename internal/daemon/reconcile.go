package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Reconcile brings the database and the engine back in line after a start,
// an engine restart or lost notifications (spec §4.4).
func (m *Manager) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list, err := m.e.List(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}
	known := make(map[string]engine.EngineStatus, len(list))
	for _, s := range list {
		known[string(s.Ref)] = s
	}
	js, err := m.st.ListJobs(ctx, store.Filter{States: []store.State{store.Queued, store.Active, store.Paused, store.RetryWait, store.Seeding}})
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}
	var down error
	left := 0
	for _, j := range js {
		if legacy(j) {
			m.stopLegacy(ctx, j)
			continue
		}
		if j.State == store.RetryWait {
			m.armRetry(j)
			continue
		}
		if down != nil {
			left++
			continue
		}
		var st engine.EngineStatus // unknown unless the engine says otherwise
		if j.EngineRef != nil {
			var ok bool
			st, ok = known[*j.EngineRef]
			if !ok {
				// List pages through buckets a download can move between,
				// so a miss is confirmed before anything is added twice.
				sts, err := m.e.Status(ctx, []engine.EngineRef{refOf(j)})
				if err != nil {
					m.log.Warn("reconcile: confirm missing download", "job", j.ID, "err", err)
					if engineDown(err) {
						down, left = err, left+1
					}
					continue
				}
				st = sts[0]
			}
			if !lostStatus(st) {
				m.apply(ctx, j, st, "")
				continue
			}
		}
		if err := m.gone(ctx, j, st); engineDown(err) {
			down, left = err, left+1
		}
	}
	if down != nil {
		return fmt.Errorf("reconcile: engine unavailable, %d jobs left for the next pass: %w", left, down)
	}
	return nil
}

// lostStatus means the engine no longer has the download, or holds only its
// removed result.
func lostStatus(st engine.EngineStatus) bool { return !st.Known || st.State == "removed" }

// gone handles a job whose download the engine no longer runs. A removed
// result in attach mode means someone else removed it, which is respected
// (undoable like any remove). In supervised mode only lazyaria removes
// downloads, so a removed result on a live row is re-added unless the row
// is already removed (our own Remove that stopped half way).
func (m *Manager) gone(ctx context.Context, j *store.Job, st engine.EngineStatus) error {
	if st.Known && st.State == "removed" {
		if m.Attach {
			now := m.now()
			m.prog.forget(j.ID)
			_, err := m.changeAs(ctx, "job.removed", j.ID, "removed-externally", "", func(x *store.Job) {
				x.State, x.RemovedAt, x.RetryAt = store.Removed, &now, nil
			})
			return err
		}
		if j.State == store.Removed {
			return nil
		}
	}
	return m.recover(ctx, j)
}

// recover re-adds a job the engine lost.
func (m *Manager) recover(ctx context.Context, j *store.Job) error {
	if j.State == store.Seeding {
		// The payload is complete; seeding does not outlive the engine.
		now := m.now()
		m.to(ctx, j, store.Done, "seeding ended with the engine", func(x *store.Job) { x.FinishedAt = &now })
		return nil
	}
	_, err := m.readd(ctx, j, j.State == store.Paused, "readded")
	if errors.Is(err, errDirMissing) {
		return nil // recorded on the job
	}
	if err != nil {
		m.log.Warn("re-add lost download", "job", j.ID, "err", err)
	}
	return err
}

// errDirMissing means readd refused because the job's directory is gone;
// the job is already in error(dir-missing).
var errDirMissing = errors.New("download directory is missing or on a different drive; mount it and retry")

// dirMissing is the unmounted-drive guard (spec G6). st_dev is not stable
// across reboots on btrfs, overlay or NFS, so a changed device counts only
// when the job had started and neither its file nor its control file is
// there any more.
func (m *Manager) dirMissing(j *store.Job) bool {
	if !m.s.Load().Reliability.UnmountedGuard {
		return false
	}
	d, err := m.dev(j.Dir)
	if err != nil {
		return true
	}
	if j.DirDev == nil || d == *j.DirDev || j.StartedAt == nil {
		return false
	}
	name := j.Name
	if j.Out != nil {
		name = *j.Out
	}
	p := filepath.Join(workDir(j), name)
	return !exists(p) && !exists(p+".aria2")
}

// readd hands j to the engine again from its stored row, dropping the old
// download's result first. why is recorded in the job's events; anything
// but an automatic retry also resets the retry count.
func (m *Manager) readd(ctx context.Context, j *store.Job, paused bool, why string) (*store.Job, error) {
	ctx = context.WithoutCancel(ctx) // see Add
	if legacy(j) {
		return j, &BadRequestError{errors.New(legacyMsg(j))}
	}
	// Every re-add path checks the drive: a retry into an unmounted mount
	// point would fill the root filesystem.
	if m.dirMissing(j) {
		kind := "dir-missing"
		msg := fmt.Sprintf("%s is missing or on a different drive; mount it and retry", j.Dir)
		m.to(ctx, j, store.Error, kind, func(x *store.Job) { x.ErrorKind, x.ErrorMsg, x.RetryAt = &kind, &msg, nil })
		return j, errDirMissing
	}
	if done, ok := m.completedOffline(ctx, j); ok {
		return done, nil
	}
	req := m.request(j, paused)
	req.Torrent = m.torrent(ctx, j)
	if j.EngineRef != nil {
		if err := m.e.Remove(ctx, refOf(j)); err != nil {
			m.log.Warn("drop old download", "job", j.ID, "err", err)
		}
	} else {
		req.GID = newGID()
	}
	// A job owns its file only once it has written bytes to it. A file it
	// does not own is never written into: continue=false makes aria2 rename
	// beside it, and a partial with its .aria2 still resumes.
	owned := ownsFile(j)
	req.NoContinue = !owned
	if owned && j.StartedAt != nil && !isBT(j.Kind) && !exists(filepath.Join(workDir(j), *j.Out)+".aria2") {
		// No control file (a server without ranges, or an unknown size), so
		// aria2 cannot resume. A true partial of ours goes to the trash and
		// the job starts over in place; anything else is left alone and the
		// new download goes beside it. Nothing is ever deleted.
		req.NoContinue = true
		p := filepath.Join(workDir(j), *j.Out)
		fi, err := os.Lstat(p)
		gone := errors.Is(err, fs.ErrNotExist)
		partial := err == nil && m.ownPartial(j, fi)
		otherDrive := err == nil && !m.sameDev(j) // the file there is not ours
		if gone || partial || otherDrive {
			// Disown first, durably: should the trash, the add or the process
			// fail after this, the job owns nothing at Out, so a file saved
			// there later is never taken for its partial.
			if _, err := m.change(ctx, j.ID, "", "", func(x *store.Job) {
				zero := int64(0)
				x.DoneBytes = &zero
			}); err != nil {
				m.log.Warn("disown before restart; downloading beside it", "job", j.ID, "err", err)
				partial = false
			}
			// The cached progress still counts the old bytes; a pause
			// before the next poll would store them and re-own Out.
			m.prog.forget(j.ID)
		}
		if partial {
			if e, err := m.trash(p); err != nil {
				m.log.Warn("trash own partial; downloading beside it", "job", j.ID, "err", err)
			} else if err := m.st.AddEvent(ctx, j.ID, "trashed-partial", e.TrashedPath); err != nil {
				m.log.Warn("record job event", "job", j.ID, "err", err)
			}
		}
	}
	ref, err := m.e.Add(ctx, req)
	if err != nil {
		return j, err
	}
	dev, derr := m.dev(j.Dir)
	state := store.Queued
	if paused {
		state = store.Paused
	}
	return m.change(ctx, j.ID, why, string(ref), func(x *store.Job) {
		r := string(ref)
		x.EngineRef, x.State, x.RetryAt = &r, state, nil
		if derr == nil {
			x.DirDev = &dev
		}
		x.ErrorKind, x.ErrorCode, x.ErrorMsg = nil, nil, nil
		if why != "retry" {
			x.RetryCount = 0
		}
	})
}

// ownPartial: fi, at j's Out, is the job's own unfinished file, safe to
// trash for a restart: same drive, a regular file, shorter than the known
// total. An unknown total cannot tell a partial from a complete file.
func (m *Manager) ownPartial(j *store.Job, fi os.FileInfo) bool {
	return m.sameDev(j) && fi.Mode().IsRegular() && j.TotalBytes != nil && *j.TotalBytes > 0 && fi.Size() < *j.TotalBytes
}

func (m *Manager) sameDev(j *store.Job) bool {
	d, err := m.dev(j.Dir)
	return err == nil && j.DirDev != nil && d == *j.DirDev
}

// ownsFile: the job has written bytes to the file at its Out.
func ownsFile(j *store.Job) bool {
	return j.Out != nil && j.DoneBytes != nil && *j.DoneBytes > 0
}

// completedOffline catches a download that finished while its completion
// went unseen: the file has its full size and no control file. Re-adding it
// would delete it and download it again.
func (m *Manager) completedOffline(ctx context.Context, j *store.Job) (*store.Job, bool) {
	// A same-size file the job never wrote to is someone else's.
	if !ownsFile(j) || j.TotalBytes == nil || *j.TotalBytes <= 0 {
		return j, false
	}
	p := filepath.Join(workDir(j), *j.Out)
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != *j.TotalBytes || exists(p+".aria2") {
		return j, false
	}
	now := m.now()
	total := *j.TotalBytes
	m.prog.forget(j.ID)
	j = m.set(ctx, j, "completed-offline", p, func(x *store.Job) {
		x.State, x.FinishedAt, x.DoneBytes, x.RetryAt = store.Done, &now, &total, nil
		x.ErrorKind, x.ErrorCode, x.ErrorMsg = nil, nil, nil
	})
	m.stampOrigin(ctx, j)
	return j, true
}
