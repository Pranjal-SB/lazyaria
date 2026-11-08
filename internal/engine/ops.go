package engine

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (e *aria2Engine) Add(ctx context.Context, req AddRequest) (EngineRef, error) {
	if err := checkOptions(req.Options); err != nil {
		return "", err
	}
	if len(req.URIs) == 0 && len(req.Torrent) == 0 {
		return "", fmt.Errorf("nothing to download: give a link or a torrent file")
	}
	// An unparseable link would keep its userinfo, so it never reaches aria2.
	// The link itself stays out of the message: it may hold credentials.
	for i, u := range req.URIs {
		if strings.HasPrefix(strings.ToLower(u), "magnet:") {
			continue
		}
		if _, err := url.Parse(u); err != nil {
			return "", fmt.Errorf("link %d is not a valid URL; check its %%-escapes and spaces, then add it again", i+1)
		}
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
	uris := splitAuth(req.URIs, req.User, req.Pass, opts)
	var gid string
	var err error
	if len(req.Torrent) > 0 {
		err = e.call(ctx, "aria2.addTorrent", &gid, base64.StdEncoding.EncodeToString(req.Torrent), uris, opts)
	} else {
		err = e.call(ctx, "aria2.addUri", &gid, uris, opts)
	}
	if err != nil {
		return "", fmt.Errorf("add download: %w", err)
	}
	return EngineRef(gid), nil
}

func (e *aria2Engine) Pause(ctx context.Context, ref EngineRef) error {
	return e.toggle(ctx, ref, "aria2.pause", "paused")
}

func (e *aria2Engine) Resume(ctx context.Context, ref EngineRef) error {
	return e.toggle(ctx, ref, "aria2.unpause", "active", "waiting")
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

func (e *aria2Engine) SetLimit(ctx context.Context, ref EngineRef, down, up int64) error {
	return e.call(ctx, "aria2.changeOption", nil, string(ref), map[string]string{
		"max-download-limit": strconv.FormatInt(down, 10),
		"max-upload-limit":   strconv.FormatInt(up, 10),
	})
}
