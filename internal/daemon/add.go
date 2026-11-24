package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/fsinfo"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

type AddParams struct {
	URIs        []string          `json:"uris,omitempty"`
	Torrent     []byte            `json:"torrent_b64,omitempty"`
	Dir         string            `json:"dir,omitempty"`
	Out         string            `json:"out,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
	StartPaused bool              `json:"start_paused,omitempty"`
	Force       bool              `json:"force,omitempty"` // skip the duplicate check
	Total       int64             `json:"total,omitempty"` // size when the caller already knows it; 0 = unknown
}

// Add stores the job, with its gid, first and only then hands it to the
// engine, so a crash in between leaves a row Reconcile can match or re-add.
// With reliability.duplicates = "skip" a duplicate returns *SkippedError
// (with the existing job for a known link) and nothing is added.
func (m *Manager) Add(ctx context.Context, p AddParams) (*store.Job, error) {
	if len(p.URIs) == 0 && len(p.Torrent) == 0 {
		return nil, &BadRequestError{errors.New("nothing to download: give a link or a torrent file")}
	}
	if err := engine.CheckLinks(p.URIs); err != nil {
		return nil, &BadRequestError{err}
	}
	for k := range p.Options {
		if !slices.Contains(engine.AllowedOptions, k) {
			return nil, engine.ErrInvalidOption{Name: k}
		}
	}
	if o, ok := p.Options["out"]; ok {
		// Out gets the name checks below; options.out would skip them.
		if p.Out == "" {
			p.Out = o
		}
		p.Options = maps.Clone(p.Options)
		delete(p.Options, "out")
	}
	if p.Out != "" {
		if k := jobKind(p.URIs, p.Torrent); isBT(k) {
			return nil, engine.ErrInvalidOption{Name: "out", Reason: "torrents and magnet links name their own files; drop --out"}
		}
	}
	dir := p.Dir
	if dir == "" {
		dir = m.s.Load().Downloads.Dir
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("download directory %q is not an absolute path; pass a full path or set downloads.dir", dir)
	}
	// The engine bounds its own calls; a caller giving up must not stop the
	// database from recording what the engine already did.
	ctx = context.WithoutCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	target := existingTarget(dir, p)
	// A .torrent file has no link; its info-hash, in magnet form, stands
	// for it so it matches itself and the same torrent's magnet.
	var keys []string
	if h := infoHash(p.Torrent); h != "" {
		keys = append(keys, textutil.MagnetKey(h))
	}
	if mode := m.s.Load().Reliability.Duplicates; !p.Force && mode != "allow" {
		if j := m.findDuplicate(ctx, append(slices.Clone(p.URIs), keys...)); j != nil {
			if mode == "skip" {
				return j, &SkippedError{Existing: j}
			}
			return nil, &DuplicateError{Existing: j}
		}
		if target != "" {
			if mode == "skip" {
				return nil, &SkippedError{Path: target}
			}
			return nil, &DuplicateError{Path: target}
		}
	}
	// The download runs in its own staging folder, so a taken name only
	// matters at publish, which picks a free one then (rename).
	overwrite := false
	if onExists := m.s.Load().Downloads.OnExists; target != "" {
		if fi, err := os.Lstat(target); onExists == "overwrite" && (err != nil || !fi.Mode().IsRegular()) {
			onExists = "rename" // only a regular file goes to the trash
		}
		switch onExists {
		case "overwrite":
			overwrite = true
		case "ask":
			return nil, &ExistsError{Path: target}
		}
	}
	if ns := m.noSpace(dir, p.Total, 0); ns != nil {
		return nil, ns
	}
	paused := p.StartPaused || m.s.Load().Downloads.StartPaused
	j := m.newJob(dir, p, paused)
	// Before the row: a crash in between leaves an orphan folder Reconcile clears.
	if err := os.MkdirAll(stagingDir(j), 0o755); err != nil {
		return nil, fmt.Errorf("create the staging folder: %w; nothing was added. Check that %s is writable", err, dir)
	}
	if err := m.st.InsertJob(ctx, j, keys...); err != nil {
		os.Remove(stagingDir(j))
		return nil, fmt.Errorf("save the new job: %w", err)
	}
	// Overwrite never reaches aria2 (allow-overwrite would pass on to a
	// torrent a link turns into): the old file goes to the trash,
	// where the user can restore it, and the add is a plain one. The row
	// comes first, so a crash right after the trash still leaves the job.
	if overwrite {
		e, err := m.trash(target)
		if err != nil {
			if derr := m.st.DeleteJob(ctx, j.ID); derr != nil {
				err = errors.Join(err, derr)
			} else {
				os.Remove(stagingDir(j))
			}
			return nil, fmt.Errorf("move %s to the trash before overwriting it: %w; nothing was added. Move it aside, or set downloads.on_exists to rename", target, err)
		}
		if err := m.st.AddEvent(ctx, j.ID, "trashed-existing", e.TrashedPath); err != nil {
			m.log.Warn("record job event", "job", j.ID, "err", err)
		}
	}
	if err := m.st.AddEvent(ctx, j.ID, "added", ""); err != nil {
		m.log.Warn("record job event", "job", j.ID, "err", err)
	}
	m.bus.Publish(Event{Type: "job.added", Job: m.decorate(j)})
	m.lifecycleFrame(ctx)
	// The staging folder is new and empty, so continue=true adopts nothing.
	req := m.request(j, paused)
	ref, err := m.e.Add(ctx, req)
	if engineDown(err) {
		// The add may or may not have landed under the job's gid; the poll
		// or Reconcile finds out and re-adds only if it did not.
		m.log.Warn("add not confirmed by the engine; the job stays queued", "job", j.ID, "err", err)
		return j, nil
	}
	if err != nil {
		kind := "add-failed"
		msg := err.Error()
		if j2, cerr := m.change(ctx, j.ID, string(store.Error), kind, func(x *store.Job) {
			x.State, x.ErrorKind, x.ErrorMsg = store.Error, &kind, &msg
		}); cerr == nil {
			j = j2
		}
		return j, err
	}
	state := store.Queued
	if paused {
		state = store.Paused
	}
	j, err = m.change(ctx, j.ID, "engine-added", string(ref), func(x *store.Job) {
		r := string(ref)
		x.EngineRef, x.State = &r, state
	})
	if err != nil {
		return nil, err
	}
	// A quick download can finish before its ref was stored, while its
	// events found no job; one status read catches that up.
	if sts, err := m.e.Status(ctx, []engine.EngineRef{ref}); err == nil && sts[0].Known {
		j = m.apply(ctx, j, sts[0], "")
	}
	return j, nil
}

func (m *Manager) findDuplicate(ctx context.Context, uris []string) *store.Job {
	for _, u := range uris {
		clean, _, _ := textutil.SplitUserinfo(u)
		norm, err := textutil.NormalizeURL(clean)
		if err != nil {
			continue
		}
		if j, err := m.st.FindByNormalizedURL(ctx, norm); err == nil {
			return j
		}
	}
	return nil
}

// noSpace checks free space against the remaining bytes plus the margin.
// An unknown size or an unreadable filesystem skips the check.
func (m *Manager) noSpace(dir string, total, done int64) *NoSpaceError {
	if !m.s.Load().Reliability.DiskPrecheck || total <= 0 || done >= total {
		return nil
	}
	free, err := m.free(dir)
	if err != nil {
		return nil
	}
	left := uint64(total - done)
	need := left + left/100*uint64(m.s.Load().Reliability.DiskMarginPercent)
	if free >= need {
		return nil
	}
	return &NoSpaceError{Dir: dir, Need: need, Free: free}
}

// newJob builds the intent row. A start-paused job is paused from the
// start, so a re-add after an unconfirmed engine add keeps it paused.
func (m *Manager) newJob(dir string, p AddParams, paused bool) *store.Job {
	opts := maps.Clone(p.Options)
	if opts == nil {
		opts = map[string]string{}
	}
	uris := engine.SplitAuth(p.URIs, "", "", opts)
	if _, set := opts["file-allocation"]; !set {
		alloc := m.s.Load().Downloads.FileAllocation
		if alloc == "auto" {
			alloc = m.alloc(dir)
		}
		opts["file-allocation"] = alloc
	}
	kind := jobKind(uris, p.Torrent)
	if isBT(kind) {
		// Per job, so a live change to the bt settings applies to new torrents.
		setDefault(opts, "seed-time", strconv.Itoa(m.s.Load().BT.SeedMinutes))
		setDefault(opts, "seed-ratio", strconv.FormatFloat(m.s.Load().BT.SeedRatio, 'f', -1, 64))
	}
	fat := fsinfo.IsFAT(dir)
	j := &store.Job{
		ID: store.NewID(), Kind: kind, Engine: "aria2", State: store.Queued,
		Name: textutil.SanitizeName(displayName(p.Out, uris), fat),
		URIs: uris, Torrent: p.Torrent, Dir: dir, Options: opts, Queue: "main", CreatedAt: m.now(),
		Staged: true,
	}
	if paused {
		j.State = store.Paused
	}
	if p.Out != "" {
		out := textutil.SanitizeName(p.Out, fat)
		j.Out = &out
	}
	gid := newGID()
	j.EngineRef = &gid
	if d, err := m.dev(dir); err == nil {
		j.DirDev = &d
	}
	if p.Total > 0 {
		j.TotalBytes = &p.Total
	}
	return j
}

func (m *Manager) request(j *store.Job, paused bool) engine.AddRequest {
	req := engine.AddRequest{URIs: j.URIs, Torrent: j.Torrent, Dir: workDir(j), Options: j.Options, Paused: paused}
	if j.Out != nil {
		req.Out = *j.Out
	}
	if j.EngineRef != nil {
		req.GID = *j.EngineRef
	}
	return req
}

func setDefault(m map[string]string, k, v string) {
	if _, ok := m[k]; !ok {
		m[k] = v
	}
}

func jobKind(uris []string, torrent []byte) string {
	if len(torrent) > 0 {
		return "torrent"
	}
	s := strings.ToLower(strings.SplitN(uris[0], ":", 2)[0])
	switch s {
	case "magnet", "ftp", "sftp":
		return s
	}
	return "http"
}

func displayName(out string, uris []string) string {
	if out != "" || len(uris) == 0 {
		return out
	}
	u, err := url.Parse(uris[0])
	if err != nil {
		return ""
	}
	if u.Scheme == "magnet" {
		return u.Query().Get("dn")
	}
	if b := path.Base(u.Path); b != "/" && b != "." {
		return b
	}
	return ""
}

func isBT(kind string) bool { return kind == "torrent" || kind == "magnet" }

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
