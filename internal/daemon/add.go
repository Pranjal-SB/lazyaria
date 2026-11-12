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
	"unicode/utf8"

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
		return nil, errors.New("nothing to download: give a link or a torrent file")
	}
	if err := engine.CheckLinks(p.URIs); err != nil {
		return nil, err
	}
	for k := range p.Options {
		if !slices.Contains(engine.AllowedOptions, k) {
			return nil, engine.ErrInvalidOption{Name: k}
		}
	}
	if p.Out != "" || p.Options["out"] != "" {
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
	if mode := m.s.Load().Reliability.Duplicates; !p.Force && mode != "allow" {
		if j := m.findDuplicate(ctx, p.URIs); j != nil {
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
	overwrite := false
	// shortcut: a server-chosen name (Content-Disposition, no URL basename)
	// is unknown here, so aria2 may adopt another live job's partial through
	// its .aria2 (probe p4); v0.2 probes the name first and writes .laria files.
	name := targetName(dir, p)
	var held map[string]bool
	if name != "" {
		var err error
		if held, err = m.heldNames(ctx, dir); err != nil {
			return nil, err
		}
	}
	if onExists := m.s.Load().Downloads.OnExists; target != "" || held[name] {
		if held[name] {
			// Another live job writes this name; any .aria2 there is its own.
			onExists = "rename"
		}
		if fi, err := os.Lstat(target); onExists == "overwrite" && (err != nil || !fi.Mode().IsRegular()) {
			onExists = "rename" // only a regular file goes to the trash
		}
		switch onExists {
		case "overwrite":
			overwrite = true
		case "ask":
			return nil, &ExistsError{Path: target}
		default: // rename
			name, err := freeName(filepath.Join(dir, name), held)
			if err != nil {
				return nil, err
			}
			p.Out = name
		}
	}
	if ns := m.noSpace(dir, p.Total, 0); ns != nil {
		return nil, ns
	}
	paused := p.StartPaused || m.s.Load().Downloads.StartPaused
	j := m.newJob(dir, p, paused)
	if err := m.st.InsertJob(ctx, j); err != nil {
		return nil, fmt.Errorf("save the new job: %w", err)
	}
	// Overwrite never reaches aria2 (allow-overwrite would pass on to a
	// torrent a link turns into, probe p6): the old file goes to the trash,
	// where the user can restore it, and the add is a plain one. The row
	// comes first, so a crash right after the trash still leaves the job.
	if overwrite {
		e, err := m.trash(target)
		if err != nil {
			if derr := m.st.DeleteJob(ctx, j.ID); derr != nil {
				err = errors.Join(err, derr)
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
	m.bus.Publish(Event{Type: "job.added", Job: j})
	req := m.request(j, paused)
	// A fresh job owns no file yet: continue=false makes aria2 rename beside
	// a finished file that appears after the check above (one with a .aria2
	// is adopted and resumed). readd decides by ownership.
	req.NoContinue = true
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
	req := engine.AddRequest{URIs: j.URIs, Torrent: j.Torrent, Dir: j.Dir, Options: j.Options, Paused: paused}
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

// existingTarget returns dir/out when a finished file already sits there:
// the file exists and has no .aria2 control file, which would mean an
// unfinished download that aria2 resumes (spec §8.4).
func existingTarget(dir string, p AddParams) string {
	name := targetName(dir, p)
	if name == "" {
		return ""
	}
	path := filepath.Join(dir, name)
	if exists(path) && !exists(path+".aria2") {
		return path
	}
	return ""
}

// targetName is the sanitized name the add will write, or "" when only the
// server or the torrent can tell.
func targetName(dir string, p AddParams) string {
	clean := make([]string, len(p.URIs))
	for i, u := range p.URIs {
		clean[i], _, _ = textutil.SplitUserinfo(u)
	}
	name := p.Out
	if name == "" && len(p.Torrent) == 0 && len(clean) > 0 && jobKind(clean, nil) != "magnet" {
		name = displayName("", clean)
	}
	if name == "" {
		return ""
	}
	return textutil.SanitizeName(name, fsinfo.IsFAT(dir))
}

func isBT(kind string) bool { return kind == "torrent" || kind == "magnet" }

// heldNames are the names live jobs in dir will write, which may not be
// on disk yet.
func (m *Manager) heldNames(ctx context.Context, dir string) (map[string]bool, error) {
	js, err := m.st.ListJobs(ctx, store.Filter{})
	if err != nil {
		return nil, fmt.Errorf("list jobs to pick a free name: %w", err)
	}
	held := map[string]bool{}
	for _, j := range js {
		if filepath.Clean(j.Dir) == filepath.Clean(dir) {
			held[j.Name] = true
			if j.Out != nil {
				held[*j.Out] = true
			}
		}
	}
	return held, nil
}

// freeName picks the name aria2 itself would rename path to: the first
// base.N.ext not on disk and not held by a live job, shortened to fit
// textutil.MaxName.
func freeName(path string, held map[string]bool) (string, error) {
	dir, name := filepath.Split(path)
	base, ext := name, ""
	if i := strings.LastIndexByte(name, '.'); i > 0 && len(name)-i <= 32 {
		base, ext = name[:i], name[i:]
	}
	for n := 1; n < 10000; n++ {
		suffix := "." + strconv.Itoa(n) + ext
		b := base
		if keep := textutil.MaxName - len(suffix); len(b) > keep {
			b = b[:keep]
			for !utf8.ValidString(b) {
				b = b[:len(b)-1]
			}
		}
		cand := filepath.Join(dir, b+suffix)
		if !held[b+suffix] && !exists(cand) && !exists(cand+".aria2") {
			return b + suffix, nil
		}
	}
	return "", fmt.Errorf("%s already exists and no free name beside it was found; choose another name with --out", path)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
