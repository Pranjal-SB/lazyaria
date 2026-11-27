package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
)

func (e *aria2Engine) Add(ctx context.Context, req AddRequest) (EngineRef, error) {
	if err := checkOptions(req.Options); err != nil {
		return "", err
	}
	if len(req.URIs) == 0 && len(req.Torrent) == 0 {
		return "", fmt.Errorf("nothing to download: give a link or a torrent file")
	}
	if err := CheckLinks(req.URIs); err != nil {
		return "", err
	}
	opts := maps.Clone(req.Options)
	if opts == nil {
		opts = map[string]string{}
	}
	if req.Dir != "" {
		opts["dir"] = req.Dir
	}
	if req.Out != "" {
		opts["out"] = req.Out
	}
	if req.Paused {
		opts["pause"] = "true"
	}
	// Per download, so an attached aria2 whose own config allows overwrite
	// still never overwrites a file.
	opts["allow-overwrite"] = "false"
	if req.GID != "" {
		opts["gid"] = req.GID
	}
	switch req.SeedFrom {
	case "unverified":
		opts["bt-seed-unverified"] = "true"
	case "check":
		opts["check-integrity"] = "true"
	}
	uris := SplitAuth(req.URIs, req.User, req.Pass, opts)
	add := func() (gid string, err error) {
		if len(req.Torrent) > 0 {
			err = e.call(ctx, "aria2.addTorrent", &gid, base64.StdEncoding.EncodeToString(req.Torrent), uris, opts)
		} else {
			err = e.call(ctx, "aria2.addUri", &gid, uris, opts)
		}
		return gid, err
	}
	gid, err := add()
	// Only a refusal is safe to retry: after a timeout the first add may
	// have gone through. A refused gid is usually still taken.
	var rpcErr *aria2.RPCError
	if err != nil && req.GID != "" && errors.As(err, &rpcErr) {
		delete(opts, "gid")
		gid, err = add()
	}
	if err != nil {
		return "", fmt.Errorf("add download: %w", err)
	}
	return EngineRef(gid), nil
}

func (e *aria2Engine) Pause(ctx context.Context, ref EngineRef) error {
	return e.toggle(ctx, ref, "aria2.pause", "paused")
}

func (e *aria2Engine) Resume(ctx context.Context, ref EngineRef) (restarted string, err error) {
	// A paused download with bytes but no .aria2 control file would trust
	// its file, preallocated to full size, and end complete with zeroes in
	// it; continue=false starts it over beside the file instead.
	var s aria2.Status
	if e.call(ctx, "aria2.tellStatus", &s, string(ref), []string{"status", "files", "bittorrent", "completedLength"}) == nil &&
		s.Status == "paused" && s.Bittorrent == nil && s.CompletedLength > 0 &&
		len(s.Files) > 0 && s.Files[0].Path != "" && !hasControlFile(s.Files[0].Path) {
		if err := e.call(ctx, "aria2.changeOption", nil, string(ref), map[string]string{"continue": "false"}); err != nil {
			return "", fmt.Errorf("aria2.unpause %s: %w", ref, err)
		}
		restarted = s.Files[0].Path
	}
	if err := e.toggle(ctx, ref, "aria2.unpause", "active", "waiting"); err != nil {
		return "", err
	}
	return restarted, nil
}

// toggle makes pause/unpause idempotent: aria2 refuses to pause a paused
// download, which is not a failure for us.
func (e *aria2Engine) toggle(ctx context.Context, ref EngineRef, method string, already ...string) error {
	err := e.call(ctx, method, nil, string(ref))
	if err == nil {
		return nil
	}
	if st, serr := e.state(ctx, ref); serr == nil && slices.Contains(already, st) {
		return nil
	}
	return fmt.Errorf("%s %s: %w", method, ref, err)
}

func (e *aria2Engine) state(ctx context.Context, ref EngineRef) (string, error) {
	var s struct{ Status string }
	err := e.call(ctx, "aria2.tellStatus", &s, string(ref), []string{"status"})
	return s.Status, err
}

// waitState polls until ref reaches one of states, for at most callTimeout
// when ctx has no deadline of its own.
func (e *aria2Engine) waitState(ctx context.Context, ref EngineRef, states ...string) (string, error) {
	ctx, cancel := withDefault(ctx)
	defer cancel()
	for {
		st, err := e.state(ctx, ref)
		if err != nil || slices.Contains(states, st) {
			return st, err
		}
		select {
		case <-ctx.Done():
			return st, fmt.Errorf("%s stayed %s: %w", ref, st, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

var stopped = []string{"complete", "error", "removed"}

// Remove stops ref if needed and drops its result, so aria2 forgets it.
// An unknown ref is already gone.
func (e *aria2Engine) Remove(ctx context.Context, ref EngineRef) error {
	st, err := e.state(ctx, ref)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove %s: %w", ref, err)
	}
	if !slices.Contains(stopped, st) {
		if err := e.stop(ctx, ref); err != nil {
			if isNotFound(err) {
				return nil
			}
			return fmt.Errorf("remove %s: %w", ref, err)
		}
	}
	if err := e.call(ctx, "aria2.removeDownloadResult", nil, string(ref)); err != nil && !isNotFound(err) {
		return fmt.Errorf("remove %s: %w", ref, err)
	}
	return nil
}

// stop removes a live download and waits until aria2 has stopped it. A
// remove that fails because the download stopped on its own is fine.
func (e *aria2Engine) stop(ctx context.Context, ref EngineRef) error {
	if err := e.call(ctx, "aria2.remove", nil, string(ref)); err != nil {
		if ferr := e.call(ctx, "aria2.forceRemove", nil, string(ref)); ferr != nil {
			st, serr := e.state(ctx, ref)
			if serr != nil {
				return serr
			}
			if !slices.Contains(stopped, st) {
				return ferr
			}
			return nil
		}
	}
	_, err := e.waitState(ctx, ref, stopped...)
	return err
}

func (e *aria2Engine) SetGlobalLimit(ctx context.Context, down, up int64) error {
	return e.call(ctx, "aria2.changeGlobalOption", nil, map[string]string{
		"max-overall-download-limit": strconv.FormatInt(down, 10),
		"max-overall-upload-limit":   strconv.FormatInt(up, 10),
	})
}

func (e *aria2Engine) SetGlobalOptions(ctx context.Context, opts map[string]string) error {
	return e.call(ctx, "aria2.changeGlobalOption", nil, opts)
}

func (e *aria2Engine) SetLimit(ctx context.Context, ref EngineRef, down, up int64) error {
	return e.call(ctx, "aria2.changeOption", nil, string(ref), map[string]string{
		"max-download-limit": strconv.FormatInt(down, 10),
		"max-upload-limit":   strconv.FormatInt(up, 10),
	})
}

// CheckLinks refuses a link url.Parse rejects: it would keep its userinfo,
// so it must reach neither aria2 nor the database. The link itself stays
// out of the message, as it may hold credentials.
func CheckLinks(uris []string) error {
	for i, u := range uris {
		bad := fmt.Errorf("link %d is not a valid URL; check its %%-escapes and spaces, then add it again", i+1)
		// A line break in a magnet would add lines to aria2's session file.
		if strings.IndexFunc(u, unicode.IsControl) >= 0 {
			return bad
		}
		if strings.HasPrefix(strings.ToLower(u), "magnet:") {
			continue
		}
		if p, err := url.Parse(u); err != nil || !slices.Contains([]string{"http", "https", "ftp", "sftp"}, strings.ToLower(p.Scheme)) {
			return bad
		}
	}
	return nil
}
