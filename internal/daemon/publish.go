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
	publishStop   = func(step string) bool { return false } // true: stop after step, as a crash would
)

var (
	errPublishStopped = errors.New("publish stopped")
	errStagingBusy    = errors.New("another download still writes into the staging folder")
)

// publish moves a complete staged job's payload into j.Dir following the
// recorded intent and returns the updated job (state done, Staged false,
// Publish nil, Out/Name = the final name). Safe to call again on a job
// whose intent is already recorded: every step can be repeated after a
// crash (decision 63). A failure leaves the intent and puts the job in
// error; resuming it publishes again.
func (m *Manager) publish(ctx context.Context, j *store.Job) (*store.Job, error) {
	staging := stagingDir(j)
	// A metalink's other files may still be downloading into the same
	// folder; the poll tries again once they are done.
	if refs, ok := m.stagingRefs(ctx, j); !ok || len(refs) > 0 {
		return j, errStagingBusy
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
			if entries[i].Name, err = finalName(j, entries[i].Rel, held); err != nil {
				return m.publishFailed(ctx, j, err)
			}
			held[entries[i].Name] = true
		}
		p := &store.Publish{Name: entries[0].Name, Entries: entries}
		if j, err = m.record(ctx, j, p); err != nil {
			return m.publishFailed(ctx, j, err)
		}
	}
	if publishStop("intent") {
		return j, errPublishStopped
	}
	p := clonePublish(j.Publish)
	for i := range p.Entries {
		var err error
		if j, err = m.publishEntry(ctx, j, p, i); err != nil {
			if errors.Is(err, errPublishStopped) {
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
	j2, err := m.change(ctx, j.ID, "published", name, func(x *store.Job) {
		x.State, x.Staged, x.Publish = store.Done, false, nil
		x.Out, x.Name = &name, name
		x.FinishedAt, x.RetryCount, x.RetryAt = &now, 0, nil
		x.ErrorKind, x.ErrorCode, x.ErrorMsg = nil, nil, nil
	})
	if err != nil {
		return m.publishFailed(ctx, j, err)
	}
	j = j2
	m.prog.forget(j.ID)
	// Everything left in staging is the job's own, by construction.
	if err := os.RemoveAll(staging); err != nil {
		m.log.Warn("remove the staging folder", "job", j.ID, "path", staging, "err", err)
	}
	os.Remove(filepath.Dir(staging)) // .laria, once no job uses it
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
			switch {
			case p.TempIno != 0 && sameFile(tmp, p.TempIno, p.TempDev, -1):
				return m.place(ctx, j, p, i, tmp, src) // copied, not yet in place
			case p.TempIno != 0 && sameFile(dst, p.TempIno, p.TempDev, -1):
				return m.copied(ctx, j, p, i, src, dst) // in place, not yet recorded
			case p.TempIno == 0 && lexists(tmp):
				// Unfinished: the temp is ours, it was recorded before it was made.
				if err := os.RemoveAll(tmp); err != nil {
					return j, err
				}
			}
			p.Temp, p.TempIno, p.TempDev = "", 0, 0
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
	tmp := filepath.Join(j.Dir, p.Temp)
	err = m.copySpace(j, src)
	if err == nil {
		err = publishCopy(src, tmp) // removes its own partial on failure
	}
	if err == nil {
		p.TempIno, p.TempDev, _, err = fsutil.FileID(tmp)
	}
	if err != nil {
		// No temp of ours is left: forget the name, so nothing found there
		// later passes for our copy.
		p.Temp, p.TempIno, p.TempDev = "", 0, 0
		j, _ = m.record(ctx, j, p)
		return j, err
	}
	if j, err = m.record(ctx, j, p); err != nil {
		return j, err
	}
	if publishStop("copied") {
		return j, errPublishStopped
	}
	return m.place(ctx, j, p, i, tmp, src)
}

// place renames the finished temp copy tmp to entry i's name, moving on to
// the next free name while one is taken; a duplicate beats a loss.
func (m *Manager) place(ctx context.Context, j *store.Job, p *store.Publish, i int, tmp, src string) (*store.Job, error) {
	for {
		dst := filepath.Join(j.Dir, p.Entries[i].Name)
		err := fsutil.Place(tmp, dst)
		if err == nil {
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

// copied records the copy in place (its identity, no temp) and then
// removes the source, so a crash in between finds the entry moved.
func (m *Manager) copied(ctx context.Context, j *store.Job, p *store.Publish, i int, src, dst string) (*store.Job, error) {
	e := &p.Entries[i]
	var err error
	if e.Ino, e.Dev, e.Size, err = fsutil.FileID(dst); err != nil {
		return j, err
	}
	p.Temp, p.TempIno, p.TempDev = "", 0, 0
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
	name, err := finalName(j, p.Entries[i].Rel, held)
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
	if ns, ok := errors.AsType[*NoSpaceError](err); ok {
		kind = "no_space"
		msg = fmt.Sprintf("the download finished in %s but %s has too little space to copy it there: need %d bytes, %d free; free some space, then resume the job", stagingDir(j), ns.Dir, ns.Need, ns.Free)
	}
	m.log.Error("publish", "job", j.ID, "err", err)
	return m.to(ctx, j, store.Error, kind, func(x *store.Job) { x.ErrorKind, x.ErrorCode, x.ErrorMsg = &kind, nil, &msg }), err
}

// publishEntries lists the top-level entries of the staging folder that
// belong to the payload: everything except *.aria2, *.torrent, *.meta4.
func publishEntries(staging string) ([]store.PublishEntry, error) {
	es, err := os.ReadDir(staging)
	if err != nil {
		return nil, err
	}
	var out []store.PublishEntry
	for _, e := range es {
		n := e.Name()
		if strings.HasSuffix(n, ".aria2") || strings.HasSuffix(n, ".torrent") || strings.HasSuffix(n, ".meta4") {
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

// tempName is the copy-fallback temp for name: .<name>.laria-copy, or a
// numbered one if something already has that name.
func tempName(dir, name string) string {
	t := "." + name + ".laria-copy"
	for n := 1; lexists(filepath.Join(dir, t)); n++ {
		t = "." + name + ".laria-copy" + strconv.Itoa(n)
	}
	return t
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
