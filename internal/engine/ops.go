package engine

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"
)

func (e *aria2Engine) Add(ctx context.Context, req AddRequest) (EngineRef, error) {
	if err := checkOptions(req.Options); err != nil {
		return "", err
	}
	if len(req.URIs) == 0 && len(req.Torrent) == 0 {
		return "", fmt.Errorf("nothing to download: give a link or a torrent file")
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

// waitState polls until ref reaches one of states.
func (e *aria2Engine) waitState(ctx context.Context, ref EngineRef, states ...string) (string, error) {
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
		if err := e.call(ctx, "aria2.remove", nil, string(ref)); err != nil {
			if err := e.call(ctx, "aria2.forceRemove", nil, string(ref)); err != nil && !isNotFound(err) {
				return fmt.Errorf("remove %s: %w", ref, err)
			}
		}
		if _, err := e.waitState(ctx, ref, stopped...); err != nil {
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
