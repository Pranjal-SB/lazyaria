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
	URIs        []string
	Torrent     []byte
	Dir, Out    string
	Options     map[string]string
	StartPaused bool
	Force       bool  // skip the duplicate check
	Total       int64 // size when the caller already knows it; 0 = unknown
}

// Add stores the job, with its gid, first and only then hands it to the
// engine, so a crash in between leaves a row Reconcile can match or re-add.
// With reliability.duplicates = "skip" a known link returns the existing
// job and a finished target file returns *SkippedError; nothing is added.
func (m *Manager) Add(ctx context.Context, p AddParams) (*store.Job, error) {
	if len(p.URIs) == 0 && len(p.Torrent) == 0 {
		return nil, errors.New("nothing to download: give a link or a torrent file")
	}
	for k := range p.Options {
		if !slices.Contains(engine.AllowedOptions, k) {
			return nil, engine.ErrInvalidOption{Name: k}
		}
	}
	dir := p.Dir
	if dir == "" {
		dir = m.s.Downloads.Dir
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("download directory %q is not an absolute path; pass a full path or set downloads.dir", dir)
	}
	// The engine bounds its own calls; a caller giving up must not stop the
	// database from recording what the engine already did.
	ctx = context.WithoutCancel(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if mode := m.s.Reliability.Duplicates; !p.Force && mode != "allow" {
		if j := m.findDuplicate(ctx, p.URIs); j != nil {
			if mode == "skip" {
				return j, nil
			}
			return nil, &DuplicateError{Existing: j}
		}
		if path := existingTarget(dir, p); path != "" {
			if mode == "skip" {
				return nil, &SkippedError{Path: path}
			}
			return nil, &DuplicateError{Path: path}
		}
	}
	if ns := m.noSpace(dir, p.Total, 0); ns != nil {
		return nil, ns
	}
	j := m.newJob(dir, p)
	if err := m.st.InsertJob(ctx, j); err != nil {
		return nil, fmt.Errorf("save the new job: %w", err)
	}
	if err := m.st.AddEvent(ctx, j.ID, "added", ""); err != nil {
		m.log.Warn("record job event", "job", j.ID, "err", err)
	}
	m.bus.Publish(Event{Type: "job.added", Job: j})
	paused := p.StartPaused || m.s.Downloads.StartPaused
	ref, err := m.e.Add(ctx, m.request(j, paused))
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
	if !m.s.Reliability.DiskPrecheck || total <= 0 || done >= total {
		return nil
	}
	free, err := m.free(dir)
	if err != nil {
		return nil
	}
	left := uint64(total - done)
	need := left + left/100*uint64(m.s.Reliability.DiskMarginPercent)
	if free >= need {
		return nil
	}
	return &NoSpaceError{Dir: dir, Need: need, Free: free}
}

func (m *Manager) newJob(dir string, p AddParams) *store.Job {
	opts := maps.Clone(p.Options)
	if opts == nil {
		opts = map[string]string{}
	}
	uris := engine.SplitAuth(p.URIs, "", "", opts)
	if _, set := opts["file-allocation"]; !set {
		alloc := m.s.Downloads.FileAllocation
		if alloc == "auto" {
			alloc = m.alloc(dir)
		}
		opts["file-allocation"] = alloc
	}
	kind := jobKind(uris, p.Torrent)
	if kind == "torrent" || kind == "magnet" {
		// Per job, so a live change to the bt settings applies to new torrents.
		setDefault(opts, "seed-time", strconv.Itoa(m.s.BT.SeedMinutes))
		setDefault(opts, "seed-ratio", strconv.FormatFloat(m.s.BT.SeedRatio, 'f', -1, 64))
	}
	fat := fsinfo.IsFAT(dir)
	j := &store.Job{
		ID: store.NewID(), Kind: kind, Engine: "aria2", State: store.Queued,
		Name: textutil.SanitizeName(displayName(p.Out, uris), fat),
		URIs: uris, Torrent: p.Torrent, Dir: dir, Options: opts, Queue: "main", CreatedAt: m.now(),
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
	path := filepath.Join(dir, textutil.SanitizeName(name, fsinfo.IsFAT(dir)))
	if exists(path) && !exists(path+".aria2") {
		return path
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
