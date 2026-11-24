package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strconv"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
)

// Status reads refs in one multicall. A ref aria2 does not know comes back
// with Known false and State "removed".
func (e *aria2Engine) Status(ctx context.Context, refs []EngineRef) ([]EngineStatus, error) {
	raw, err := e.tell(ctx, refs, aria2.StatusKeys)
	if err != nil {
		return nil, err
	}
	return e.finish(ctx, raw, refs)
}

// listPage is how many waiting or stopped downloads one List request reads.
var listPage = 1000

// List returns every download aria2 holds: active, waiting and stopped.
// Waiting and stopped are paged until a short page, one multicall per page,
// so nothing past aria2's per-call limit is mistaken for unknown.
func (e *aria2Engine) List(ctx context.Context) ([]EngineStatus, error) {
	c, err := e.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := withDefault(ctx)
	defer cancel()
	var raw []*aria2.Status
	var refs []EngineRef
	seen := map[string]bool{} // a download can move buckets between pages
	more := []string{"aria2.tellWaiting", "aria2.tellStopped"}
	for off := 0; len(more) > 0; off += listPage {
		var calls []aria2.Call
		if off == 0 {
			calls = append(calls, aria2.Call{Method: "aria2.tellActive", Params: []any{aria2.StatusKeys}})
		}
		for _, m := range more {
			calls = append(calls, aria2.Call{Method: m, Params: []any{off, listPage, aria2.StatusKeys}})
		}
		res, err := c.Multicall(ctx, calls)
		if err != nil {
			return nil, fmt.Errorf("list downloads: %w", err)
		}
		var next []string
		for i, r := range res {
			if r.Err != nil {
				return nil, fmt.Errorf("list downloads: %w", r.Err)
			}
			var part []*aria2.Status
			if err := json.Unmarshal(r.Raw, &part); err != nil {
				return nil, fmt.Errorf("list downloads: %w", err)
			}
			for _, s := range part {
				if !seen[s.GID] {
					seen[s.GID] = true
					raw = append(raw, s)
					refs = append(refs, EngineRef(s.GID))
				}
			}
			if calls[i].Method != "aria2.tellActive" && len(part) == listPage {
				next = append(next, calls[i].Method)
			}
		}
		more = next
	}
	return e.finish(ctx, raw, refs)
}

// finish maps raw statuses. Files are heavy for torrents, so they are
// fetched in a second call, and only for non-torrent downloads, which need
// them for a name.
func (e *aria2Engine) finish(ctx context.Context, raw []*aria2.Status, refs []EngineRef) ([]EngineStatus, error) {
	var need []EngineRef
	var at []int
	for i, s := range raw {
		if s != nil && s.Bittorrent == nil {
			need = append(need, refs[i])
			at = append(at, i)
		}
	}
	if len(need) > 0 {
		files, err := e.tell(ctx, need, []string{"files"})
		if err != nil {
			return nil, err
		}
		for j, f := range files {
			if f != nil {
				raw[at[j]].Files = f.Files
			}
		}
	}
	out := make([]EngineStatus, len(raw))
	for i, s := range raw {
		if s == nil {
			out[i] = EngineStatus{Ref: refs[i], State: "removed"}
			continue
		}
		out[i] = toStatus(s)
	}
	return out, nil
}

func withDefault(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, callTimeout)
}

// tell runs tellStatus for each ref; an unknown ref gives a nil entry.
func (e *aria2Engine) tell(ctx context.Context, refs []EngineRef, keys []string) ([]*aria2.Status, error) {
	c, err := e.client()
	if err != nil {
		return nil, err
	}
	ctx, cancel := withDefault(ctx)
	defer cancel()
	calls := make([]aria2.Call, len(refs))
	for i, r := range refs {
		calls[i] = aria2.Call{Method: "aria2.tellStatus", Params: []any{string(r), keys}}
	}
	res, err := c.Multicall(ctx, calls)
	if err != nil {
		return nil, fmt.Errorf("read status: %w", err)
	}
	out := make([]*aria2.Status, len(res))
	for i, r := range res {
		if r.Err != nil {
			if isNotFound(r.Err) {
				continue
			}
			return nil, fmt.Errorf("read status of %s: %w", refs[i], r.Err)
		}
		out[i] = new(aria2.Status)
		if err := json.Unmarshal(r.Raw, out[i]); err != nil {
			return nil, fmt.Errorf("read status of %s: %w", refs[i], err)
		}
	}
	return out, nil
}

func toStatus(s *aria2.Status) EngineStatus {
	st := EngineStatus{
		Ref: EngineRef(s.GID), State: s.Status,
		Total: int64(s.TotalLength), Done: int64(s.CompletedLength),
		Down: int64(s.DownloadSpeed), Up: int64(s.UploadSpeed),
		Conns: int64(s.Connections), Seeders: int64(s.NumSeeders),
		ErrorMsg:   s.ErrorMessage,
		HTTPStatus: aria2.HTTPStatus(s.ErrorMessage),
		IsBT:       s.Bittorrent != nil,
		Known:      true,
		Seeder:     s.Seeder == "true",
	}
	st.ErrorCode, _ = strconv.Atoi(s.ErrorCode)
	if len(s.FollowedBy) > 0 {
		st.FollowedBy = EngineRef(s.FollowedBy[0])
	}
	switch {
	case s.Bittorrent != nil && s.Bittorrent.Info.Name != "":
		st.Name = s.Bittorrent.Info.Name
		st.Path = filepath.Join(s.Dir, st.Name)
	case len(s.Files) > 0 && s.Files[0].Path != "":
		st.Path = s.Files[0].Path
		st.Name = filepath.Base(st.Path)
	case len(s.Files) > 0 && len(s.Files[0].URIs) > 0:
		if u, err := url.Parse(s.Files[0].URIs[0].URI); err == nil {
			st.Name = path.Base(u.Path)
		}
	}
	if st.Name == "" || st.Name == "." || st.Name == "/" {
		st.Name = "download"
	}
	return st
}

func (e *aria2Engine) Details(ctx context.Context, ref EngineRef) (EngineDetails, error) {
	raw, err := e.tell(ctx, []EngineRef{ref}, append([]string{"files", "bitfield", "numPieces"}, aria2.StatusKeys...))
	if err != nil {
		return EngineDetails{}, err
	}
	if raw[0] == nil {
		return EngineDetails{}, fmt.Errorf("download %s is not known to aria2; it may have been removed", ref)
	}
	s := raw[0]
	d := EngineDetails{Status: toStatus(s), Bitfield: s.Bitfield, Pieces: int(s.NumPieces)}
	for _, f := range s.Files {
		d.Files = append(d.Files, EngineFile{Path: f.Path, Length: int64(f.Length), Done: int64(f.CompletedLength), Selected: f.Selected == "true"})
	}
	if s.Status != "active" {
		return d, nil
	}
	// Servers and peers exist only while active; a failure here just leaves them empty.
	if d.Status.IsBT {
		var peers []aria2.Peer
		if e.call(ctx, "aria2.getPeers", &peers, string(ref)) == nil {
			for _, p := range peers {
				d.Peers = append(d.Peers, EnginePeer{IP: p.IP, Port: int64(p.Port), Down: int64(p.DownloadSpeed), Up: int64(p.UploadSpeed), Seeder: p.Seeder == "true"})
			}
		}
		return d, nil
	}
	var servers []struct{ Servers []aria2.Server }
	if e.call(ctx, "aria2.getServers", &servers, string(ref)) == nil {
		for _, f := range servers {
			for _, sv := range f.Servers {
				d.Servers = append(d.Servers, EngineServer{URI: sv.CurrentURI, Down: int64(sv.DownloadSpeed)})
			}
		}
	}
	return d, nil
}
