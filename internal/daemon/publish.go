package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/fsinfo"
	"github.com/Pranjal-SB/lazyaria/internal/fsutil"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// Test hooks: tests inject EXDEV and crashes through these.
var (
	publishRename = fsutil.RenameNoReplace
	publishCopy   = fsutil.CopyTree
	// The temp copy into place, and its folder-by-folder fallback.
	publishRenameTemp = fsutil.RenameNoReplace
	publishMoveInto   = fsutil.MoveInto
	publishStop       = func(step string) bool { return false } // true: stop after step, as a crash would
	publishLink       = os.Link
)

// upDir is where a seeding torrent's hard-linked copy is built in its
// staging folder, so one rename puts the whole of it in the download folder.
const upDir = ".up"

var (
	errPublishStopped = errors.New("publish stopped")
	errStagingBusy    = errors.New("another download still writes into the staging folder")
	errCopying        = errors.New("the finished download is being copied into the download folder")
)

// publish moves a complete staged job's payload into j.Dir following the
// recorded intent and returns the updated job (state done, Staged false,
// Out/Name = the first entry's final name). The finished row keeps Publish
// as the record of every entry it put in Dir. Safe to call again on a job
// whose intent is already recorded: every step can be repeated after a
// crash. A failure leaves the intent and puts the job in
// error; resuming it publishes again.
func (m *Manager) publish(ctx context.Context, j *store.Job) (*store.Job, error) {
	staging := stagingDir(j)
	if m.copying[j.ID] {
		return j, errCopying
	}
	// A metalink's other files may still be downloading into the same
	// folder; the poll tries again once they are done.
	if refs, ok := m.stagingRefs(ctx, j); !ok || len(refs) > 0 {
		return j, errStagingBusy
	}
	// A missing staging folder is a drive not mounted, not a finished move:
	// the intent stays until it is back.
	if !lexists(staging) && !allPublished(j) {
		return m.publishFailed(ctx, j, fmt.Errorf("staging folder %s: %w", staging, errDirMissing))
	}
	if j.Publish == nil {
		entries, err := publishEntries(staging)
		if err != nil {
			return m.publishFailed(ctx, j, err)
		}
		if len(entries) == 0 {
			kind, msg := "empty", "the download finished but produced no files (a metalink with invalid names does this); nothing was moved"
			return m.to(ctx, j, store.Error, kind, func(x *store.Job) { x.ErrorKind, x.ErrorCode, x.ErrorMsg = &kind, nil, &msg }), nil
		}
		held := map[string]bool{}
		for i := range entries {
			if entries[i].Name, err = finalName(j, chosenName(j, entries[i].Rel), held); err != nil {
				return m.publishFailed(ctx, j, err)
			}
			held[entries[i].Name] = true
		}
		p := &store.Publish{Name: entries[0].Name, Entries: entries}
		if j, err = m.record(ctx, j, p); err != nil {
			return m.publishFailed(ctx, j, err)
		}
	}
	unmark(j)
	if publishStop("intent") {
		return j, errPublishStopped
	}
	p := clonePublish(j.Publish)
	for i := range p.Entries {
		var err error
		if j, err = m.publishEntry(ctx, j, p, i); err != nil {
			if errors.Is(err, errPublishStopped) || errors.Is(err, errCopying) {
				return j, err
			}
			return m.publishFailed(ctx, j, err)
		}
		if publishStop("moved") {
			return j, errPublishStopped
		}
	}
	if err := fsutil.SyncDir(j.Dir); err != nil {
		m.log.Warn("sync the download folder after publish", "job", j.ID, "dir", j.Dir, "err", err)
	}
	name, now := p.Name, m.now()
	p.Temp, p.TempIno, p.TempDev, p.Building = "", 0, 0, ""
	j2, err := m.change(ctx, j.ID, "published", name, func(x *store.Job) {
		x.State, x.Staged, x.Publish, x.Name = store.Done, false, p, name
		if p.Seed {
			x.State, x.Staged = store.Seeding, true // aria2 seeds on from staging
		} else {
			x.Out = &name
		}
		x.FinishedAt, x.RetryCount, x.RetryAt = &now, 0, nil
		x.ErrorKind, x.ErrorCode, x.ErrorMsg = nil, nil, nil
	})
	if err != nil {
		return m.publishFailed(ctx, j, err)
	}
	j = j2
	if p.Seed {
		os.Remove(filepath.Join(staging, upDir)) // empty once renamed
		return j, nil
	}
	m.prog.forget(j.ID)
	m.dropStaging(j)
	m.stampOrigin(ctx, j)
	return j, nil
}

// publishEntry moves entry i of p into j.Dir: rename, or copy where the
// rename crosses filesystems. Each name and temp is recorded before use,
// and an earlier attempt's work is recognised only by inode and device.
func (m *Manager) publishEntry(ctx context.Context, j *store.Job, p *store.Publish, i int) (*store.Job, error) {
	e := &p.Entries[i]
	src := filepath.Join(stagingDir(j), e.Rel)
	for {
		dst := filepath.Join(j.Dir, e.Name)
		if sameFile(dst, e.Ino, e.Dev, e.Size) {
			return j, nil // moved before a crash
		}
		if p.Temp != "" { // a copy of this entry was under way
			tmp := filepath.Join(j.Dir, p.Temp)
			building := p.Building != "" && p.Building == e.Name && isDir(dst)
			switch {
			case building && p.TempIno != 0 && sameFile(tmp, p.TempIno, p.TempDev, -1):
				return m.build(ctx, j, p, i, tmp, src, true) // half built
			case building && !lexists(tmp):
				return m.copied(ctx, j, p, i, src, dst) // built, not yet recorded
			case p.TempIno != 0 && sameFile(tmp, p.TempIno, p.TempDev, -1):
				return m.place(ctx, j, p, i, tmp, src) // copied, not yet in place
			case p.TempIno != 0 && sameFile(dst, p.TempIno, p.TempDev, -1):
				return m.copied(ctx, j, p, i, src, dst) // in place, not yet recorded
			case p.TempIno == 0 && lexists(tmp):
				// Unfinished, and recorded before it was made, so most likely
				// ours; the trash keeps it in case it is not.
				if _, err := m.trash(tmp); err != nil {
					return j, err
				}
			}
			p.Temp, p.TempIno, p.TempDev, p.Building = "", 0, 0, ""
		}
		if !lexists(src) {
			m.log.Warn("a finished file left the staging folder before publish; nothing to move", "job", j.ID, "path", src)
			return j, nil
		}
		err := publishRename(src, dst)
		switch {
		case err == nil:
			return j, nil
		case errors.Is(err, fs.ErrExist):
			if j, err = m.nextName(ctx, j, p, i); err != nil {
				return j, err
			}
		case fsutil.IsCrossDevice(err):
			return m.copyEntry(ctx, j, p, i, src)
		default:
			return j, err
		}
	}
}

// copyEntry copies src into j.Dir under a recorded temp name, records the
// copy's identity, then puts it in place.
func (m *Manager) copyEntry(ctx context.Context, j *store.Job, p *store.Publish, i int, src string) (*store.Job, error) {
	p.Temp, p.TempIno, p.TempDev = tempName(j.Dir, p.Entries[i].Name), 0, 0
	j, err := m.record(ctx, j, p)
	if err != nil {
		return j, err
	}
	if publishStop("copy-temp") {
		return j, errPublishStopped
	}
	if err := m.copySpace(j, src); err != nil {
		return m.forgetTemp(ctx, j, p, err)
	}
	// The copy may take minutes: it runs without mu, and then continues
	// the publish from the recorded intent.
	m.copying[j.ID] = true
	m.copies.Add(1)
	go m.copyThenPublish(j.ID, src, filepath.Join(j.Dir, p.Temp))
	return j, errCopying
}

// copyThenPublish copies src to tmp without holding mu, then records the
// copy's identity and carries on with the publish.
func (m *Manager) copyThenPublish(id, src, tmp string) {
	defer m.copies.Done()
	err := publishCopy(src, tmp) // removes its own partial on failure
	var ino, dev uint64
	if err == nil {
		ino, dev, _, err = fsutil.FileID(tmp)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.copying, id)
	ctx := context.WithoutCancel(m.ctx)
	j, gerr := m.st.GetJob(ctx, id)
	if gerr != nil || !publishing(j) || j.Publish.Temp != filepath.Base(tmp) {
		m.log.Warn("the job changed while its download was copied; the copy goes to the trash", "job", id, "path", tmp)
		if err == nil {
			m.trash(tmp)
		}
		return
	}
	p := clonePublish(j.Publish)
	if err != nil {
		if j, err = m.forgetTemp(ctx, j, p, err); err != nil {
			m.publishFailed(ctx, j, err)
		}
		return
	}
	p.TempIno, p.TempDev = ino, dev
	if j, err = m.record(ctx, j, p); err != nil {
		m.publishFailed(ctx, j, err)
		return
	}
	if publishStop("copied") {
		return
	}
	m.publish(ctx, j)
}

// forgetTemp clears the recorded temp after a failed copy: none of ours is
// left, and nothing found at that name later may pass for our copy.
func (m *Manager) forgetTemp(ctx context.Context, j *store.Job, p *store.Publish, err error) (*store.Job, error) {
	p.Temp, p.TempIno, p.TempDev = "", 0, 0
	j, _ = m.record(ctx, j, p)
	return j, err
}

// place renames the finished temp copy tmp to entry i's name, moving on to
// the next free name while one is taken; a duplicate beats a loss.
func (m *Manager) place(ctx context.Context, j *store.Job, p *store.Publish, i int, tmp, src string) (*store.Job, error) {
	for {
		dst := filepath.Join(j.Dir, p.Entries[i].Name)
		err := publishRenameTemp(tmp, dst)
		if fsutil.IsCrossDevice(err) && isDir(tmp) {
			return m.build(ctx, j, p, i, tmp, src, false)
		}
		if err == nil {
			if err := fsutil.SyncDir(j.Dir); err != nil {
				return j, err
			}
			return m.copied(ctx, j, p, i, src, dst)
		}
		if !errors.Is(err, fs.ErrExist) {
			return j, err
		}
		if j, err = m.nextName(ctx, j, p, i); err != nil {
			return j, err
		}
	}
}

// build makes entry i's folder in j.Dir from the temp copy tmp file by
// file, for overlayfs, which refuses to rename a folder. The folder is
// recorded before it is made, so a resume continues into it rather than
// starting a second copy, and a failure leaves it as it is.
func (m *Manager) build(ctx context.Context, j *store.Job, p *store.Publish, i int, tmp, src string, resume bool) (*store.Job, error) {
	for {
		dst := filepath.Join(j.Dir, p.Entries[i].Name)
		if !resume {
			if lexists(dst) {
				var err error
				if j, err = m.nextName(ctx, j, p, i); err != nil {
					return j, err
				}
				continue
			}
			p.Building = p.Entries[i].Name
			var err error
			if j, err = m.record(ctx, j, p); err != nil {
				return j, err
			}
		}
		if err := publishMoveInto(tmp, dst, resume); err != nil {
			return j, err
		}
		return m.copied(ctx, j, p, i, src, dst)
	}
}

// copied records the copy in place (its identity, no temp) and then
// removes the source, so a crash in between finds the entry moved.
func (m *Manager) copied(ctx context.Context, j *store.Job, p *store.Publish, i int, src, dst string) (*store.Job, error) {
	e := &p.Entries[i]
	var err error
	if e.Ino, e.Dev, e.Size, err = fsutil.FileID(dst); err != nil {
		return j, err
	}
	p.Temp, p.TempIno, p.TempDev, p.Building = "", 0, 0, ""
	if j, err = m.record(ctx, j, p); err != nil {
		return j, err
	}
	if err := os.RemoveAll(src); err != nil {
		m.log.Warn("remove the staged copy after publishing", "job", j.ID, "path", src, "err", err)
	}
	return j, nil
}

// copySpace checks the download folder can take a copy of src, with the
// margin the copy fallback needs (twice its size).
func (m *Manager) copySpace(j *store.Job, src string) error {
	var size uint64
	filepath.WalkDir(src, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				size += uint64(fi.Size())
			}
		}
		return nil
	})
	free, err := m.free(j.Dir)
	if err != nil || free >= 2*size {
		return nil
	}
	return &NoSpaceError{Dir: j.Dir, Need: 2 * size, Free: free}
}

// nextName records the next free name for entry i after its name was taken.
func (m *Manager) nextName(ctx context.Context, j *store.Job, p *store.Publish, i int) (*store.Job, error) {
	held := map[string]bool{}
	for _, e := range p.Entries {
		held[e.Name] = true
	}
	name, err := finalName(j, chosenName(j, p.Entries[i].Rel), held)
	if err != nil {
		return j, err
	}
	p.Entries[i].Name = name
	if i == 0 {
		p.Name = name
	}
	return m.record(ctx, j, p)
}

// record stores p as j's intent; nothing else changes.
func (m *Manager) record(ctx context.Context, j *store.Job, p *store.Publish) (*store.Job, error) {
	c := clonePublish(p)
	j2, err := m.change(ctx, j.ID, "", "", func(x *store.Job) { x.Publish = c })
	if err != nil {
		return j, err
	}
	return j2, nil
}

func (m *Manager) publishFailed(ctx context.Context, j *store.Job, err error) (*store.Job, error) {
	kind := "publish-failed"
	msg := fmt.Sprintf("the download finished in %s but moving it into %s failed: %v; fix the folder, then resume the job", stagingDir(j), j.Dir, err)
	if errors.Is(err, errDirMissing) {
		kind = "dir-missing"
		msg = fmt.Sprintf("the finished download's folder %s is missing or on a different drive; mount it, then resume the job", stagingDir(j))
	} else if ns, ok := errors.AsType[*NoSpaceError](err); ok {
		kind = "no_space"
		msg = fmt.Sprintf("the download finished in %s but %s has too little space to copy it there: need %d bytes, %d free; free some space, then resume the job", stagingDir(j), ns.Dir, ns.Need, ns.Free)
	}
	m.log.Error("publish", "job", j.ID, "err", err)
	return m.to(ctx, j, store.Error, kind, func(x *store.Job) { x.ErrorKind, x.ErrorCode, x.ErrorMsg = &kind, nil, &msg }), err
}

// publishEntries lists the top-level entries of the staging folder that
// belong to the payload: everything except *.aria2, *.torrent, *.meta4 and
// the link tree of a torrent's publish.
func publishEntries(staging string) ([]store.PublishEntry, error) {
	es, err := os.ReadDir(staging)
	if err != nil {
		return nil, err
	}
	var out []store.PublishEntry
	for _, e := range es {
		n := e.Name()
		if n == upDir || strings.HasSuffix(n, ".aria2") || strings.HasSuffix(n, ".torrent") || strings.HasSuffix(n, ".meta4") {
			continue
		}
		ino, dev, size, err := fsutil.FileID(filepath.Join(staging, n))
		if err != nil {
			return nil, err
		}
		out = append(out, store.PublishEntry{Rel: n, Size: size, Ino: ino, Dev: dev})
	}
	return out, nil
}

// finalName picks the name to publish entry under: cleaned like any name
// lazyaria writes (a server's leading dots go; a user's --out was cleaned
// the same way at add), then the next free one if taken in j.Dir or held.
func finalName(j *store.Job, entry string, held map[string]bool) (string, error) {
	name := textutil.SanitizeName(entry, fsinfo.IsFAT(j.Dir))
	if name == "" {
		name = "download"
	}
	if !held[name] && !lexists(filepath.Join(j.Dir, name)) {
		return name, nil
	}
	return freeName(filepath.Join(j.Dir, name), held)
}

// chosenName is the name staged entry rel is published under, before any
// clash: the job's own name for the file aria2 wrote, which may be
// name.1.ext after aria2 started over (a server without ranges), else rel.
func chosenName(j *store.Job, rel string) string {
	if !isBT(j.Kind) && j.Out != nil && rel == *j.Out && j.Name != "" {
		return j.Name
	}
	return rel
}

// tempName is the copy-fallback temp for name: .<name>.laria-copy, or a
// numbered one if something already has that name.
func tempName(dir, name string) string {
	t := "." + name + ".laria-copy"
	for n := 1; lexists(filepath.Join(dir, t)); n++ {
		t = "." + name + ".laria-copy" + strconv.Itoa(n)
	}
	return t
}

// publishing: j is finished and its move into Dir is recorded but not
// done. A finished row keeps Publish too, with Staged false, and so does a
// torrent seeding from staging once published by links.
func publishing(j *store.Job) bool { return j.Staged && j.Publish != nil && j.State != store.Seeding }

// published lists the entries of a finished job's record that are still
// in Dir as it put them there (same inode, device and size).
func published(j *store.Job) []string {
	var out []string
	if j.Staged || j.Publish == nil {
		return nil
	}
	for _, e := range j.Publish.Entries {
		if p := filepath.Join(j.Dir, e.Name); sameFile(p, e.Ino, e.Dev, e.Size) {
			out = append(out, p)
		}
	}
	return out
}

// allPublished: every entry of j's recorded intent is already in Dir.
func allPublished(j *store.Job) bool {
	if j.Publish == nil {
		return false
	}
	for _, e := range j.Publish.Entries {
		if !sameFile(filepath.Join(j.Dir, e.Name), e.Ino, e.Dev, e.Size) {
			return false
		}
	}
	return true
}

// sameFile: path is the file with this inode and device, and this size
// unless size is -1.
func sameFile(path string, ino, dev uint64, size int64) bool {
	i, d, sz, err := fsutil.FileID(path)
	return err == nil && i == ino && d == dev && (size < 0 || sz == size)
}

func clonePublish(p *store.Publish) *store.Publish {
	c := *p
	c.Entries = slices.Clone(p.Entries)
	return &c
}
