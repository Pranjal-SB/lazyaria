package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
		if j.Publish != nil { // a publish a crash interrupted
			if m.dirMissing(j) {
				m.markDirMissing(ctx, j)
			} else {
				m.publish(ctx, j)
			}
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
	m.trashOrphans(ctx, js)
	if down != nil {
		return fmt.Errorf("reconcile: engine unavailable, %d jobs left for the next pass: %w", left, down)
	}
	return nil
}

// trashOrphans moves to the trash every staging folder whose job is gone
// (a crash between creating it and storing the row, or a lost row) or is
// no longer staged (left by a crash after publishing). It looks in the
// folders of live jobs and in downloads.dir.
func (m *Manager) trashOrphans(ctx context.Context, live []*store.Job) {
	dirs := map[string]bool{m.s.Load().Downloads.Dir: true}
	for _, j := range live {
		dirs[j.Dir] = true
	}
	for d := range dirs {
		if !filepath.IsAbs(d) {
			continue
		}
		root := filepath.Join(d, ".laria")
		es, _ := os.ReadDir(root)
		for _, e := range es {
			if !e.IsDir() {
				continue
			}
			j, err := m.st.GetJob(ctx, e.Name())
			if err == nil && j.Staged || err != nil && !errors.Is(err, store.ErrNotFound) {
				continue
			}
			// With no row, the folder may be another lazyaria's that shares
			// this download folder and has just created it.
			if fi, ferr := e.Info(); err != nil && (ferr != nil || m.now().Sub(fi.ModTime()) < time.Hour) {
				continue
			}
			p := filepath.Join(root, e.Name())
			if te, err := m.trash(p); err != nil {
				m.log.Warn("trash a staging folder no job owns", "path", p, "err", err)
			} else {
				m.log.Info("trashed a staging folder no job owns", "path", p, "trashed", te.TrashedPath)
			}
		}
	}
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
	if j.Publish != nil { // its first entry may already be in Dir
		return !lexists(stagingDir(j)) && !allPublished(j)
	}
	name := j.Name
	if j.Out != nil {
		name = *j.Out
	}
	p := filepath.Join(workDir(j), name)
	return !exists(p) && !exists(p+".aria2")
}

func (m *Manager) markDirMissing(ctx context.Context, j *store.Job) {
	kind := "dir-missing"
	msg := fmt.Sprintf("%s is missing or on a different drive; mount it and retry", j.Dir)
	m.to(ctx, j, store.Error, kind, func(x *store.Job) { x.ErrorKind, x.ErrorMsg, x.RetryAt = &kind, &msg, nil })
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
		m.markDirMissing(ctx, j)
		return j, errDirMissing
	}
	if j.Publish != nil { // finished; only the move into Dir is left
		return m.publish(ctx, j)
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
	emptied := j.Staged && !isBT(j.Kind) && !resumable(j)
	if emptied {
		// Nothing to resume from: start over in an empty staging folder
		// (decision 64). The old download was dropped above.
		if err := emptyStaging(j); err != nil {
			return j, fmt.Errorf("empty the staging folder %s to start over: %w", stagingDir(j), err)
		}
		m.prog.forget(j.ID)
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
		if emptied {
			zero := int64(0)
			x.DoneBytes = &zero
		}
		if why != "retry" {
			x.RetryCount = 0
		}
	})
}

// completedOffline catches a staged download that finished while its
// completion went unseen: its file has the known full size and no control
// file. It is published instead of downloaded again.
func (m *Manager) completedOffline(ctx context.Context, j *store.Job) (*store.Job, bool) {
	if !j.Staged || isBT(j.Kind) || j.Out == nil || j.TotalBytes == nil || *j.TotalBytes <= 0 {
		return j, false
	}
	p := filepath.Join(stagingDir(j), *j.Out)
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != *j.TotalBytes || exists(p+".aria2") {
		return j, false
	}
	total := *j.TotalBytes
	m.prog.forget(j.ID)
	j = m.set(ctx, j, "completed-offline", p, func(x *store.Job) { x.DoneBytes = &total })
	j, _ = m.publish(ctx, j) // a failure is recorded on the job
	return j, true
}
