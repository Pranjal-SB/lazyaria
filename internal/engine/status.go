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

// Status reads refs in one multicall. Files are heavy for torrents, so they
// are fetched in a second call, and only for non-torrent downloads, which
// need them for a name. A ref aria2 does not know reads as "removed".
func (e *aria2Engine) Status(ctx context.Context, refs []EngineRef) ([]EngineStatus, error) {
	raw, err := e.tell(ctx, refs, aria2.StatusKeys)
	if err != nil {
		return nil, err
	}
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
	out := make([]EngineStatus, len(refs))
	for i, s := range raw {
		if s == nil {
			out[i] = EngineStatus{Ref: refs[i], State: "removed", ErrorMsg: "not known to aria2"}
			continue
		}
		out[i] = toStatus(s)
	}
	return out, nil
}

// tell runs tellStatus for each ref; an unknown ref gives a nil entry.
func (e *aria2Engine) tell(ctx context.Context, refs []EngineRef, keys []string) ([]*aria2.Status, error) {
	c, err := e.client()
	if err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}
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
	return st
}

func (e *aria2Engine) Details(ctx context.Context, ref EngineRef) (EngineDetails, error) {
	raw, err := e.tell(ctx, []EngineRef{ref}, append([]string{"files"}, aria2.StatusKeys...))
	if err != nil {
		return EngineDetails{}, err
	}
	if raw[0] == nil {
		return EngineDetails{}, fmt.Errorf("download %s is not known to aria2; it may have been removed", ref)
	}
	s := raw[0]
	d := EngineDetails{Status: toStatus(s)}
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
