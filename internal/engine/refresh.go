package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
)

// errStopped means the download stopped with an error while we waited for
// the pause; it is refreshed by re-adding instead.
var errStopped = errors.New("download stopped during refresh")

// ChangeURI points ref at a fresh link. A live download keeps its ref and
// switches URI in place; a stopped one is re-added under the same gid, and
// its .aria2 control file makes it resume. Only if aria2 refuses the old gid
// does the download come back under a new ref.
func (e *aria2Engine) ChangeURI(ctx context.Context, ref EngineRef, uri string) (EngineRef, error) {
	if err := checkRefreshURI(uri); err != nil {
		return "", err
	}
	auth := map[string]string{}
	clean := SplitAuth([]string{uri}, "", "", auth)[0]
	for {
		var s aria2.Status
		if err := e.call(ctx, "aria2.tellStatus", &s, string(ref), []string{"status", "dir", "files", "bittorrent", "completedLength"}); err != nil {
			return "", fmt.Errorf("refresh %s: %w", ref, err)
		}
		if s.Bittorrent != nil {
			return "", ErrNotSupported
		}
		switch s.Status {
		case "active", "waiting", "paused":
			err := e.swapURI(ctx, ref, s.Status, clean, auth)
			if errors.Is(err, errStopped) {
				continue // reread dir and files, then take the re-add path
			}
			if err != nil && !errors.Is(err, ErrAlreadyComplete) {
				err = fmt.Errorf("refresh %s: %w", ref, err)
			}
			if err != nil {
				return "", err
			}
			return ref, nil
		case "error":
			return e.readd(ctx, ref, &s, clean, auth)
		case "removed":
			return "", fmt.Errorf("refresh %s: the download was removed; add the link again", ref)
		}
		return "", ErrAlreadyComplete
	}
}

// swapURI does pause, changeUri, unpause. A user-paused download stays paused.
func (e *aria2Engine) swapURI(ctx context.Context, ref EngineRef, state, uri string, auth map[string]string) (err error) {
	if state != "paused" {
		if err := e.call(ctx, "aria2.pause", nil, string(ref)); err != nil {
			return err
		}
		// Never leave a download we paused stuck paused, even on failure.
		// Only a download that finished or failed meanwhile is left alone.
		keepStopped := false
		defer func() {
			if keepStopped {
				return
			}
			if uerr := e.call(context.WithoutCancel(ctx), "aria2.unpause", nil, string(ref)); err == nil {
				err = uerr
			}
		}()
		// unpause only works once the pause has finished; the download may
		// also finish or fail first.
		st, werr := e.waitState(ctx, ref, "paused", "complete", "error", "removed")
		switch {
		case werr != nil:
			return werr
		case st == "complete":
			keepStopped = true
			return ErrAlreadyComplete
		case st == "error":
			keepStopped = true
			return errStopped
		case st == "removed":
			return errors.New("the download was removed while refreshing")
		}
	}
	// Read the list only now: aria2 repeats a URI to fill split and returns
	// in-use copies to the list on pause, so an earlier read misses some.
	var cur []aria2.URI
	if err := e.call(ctx, "aria2.getUris", &cur, string(ref)); err != nil {
		return err
	}
	old := make([]string, len(cur))
	for i, u := range cur {
		old[i] = u.URI
	}
	if err := e.call(ctx, "aria2.changeUri", nil, string(ref), 1, old, []string{uri}, 0); err != nil {
		return err
	}
	if len(auth) > 0 {
		return e.call(ctx, "aria2.changeOption", nil, string(ref), auth)
	}
	return nil
}

// alwaysCopy are kept on re-add even when they equal the global value.
var alwaysCopy = []string{"header", "http-user", "http-passwd", "ftp-user", "ftp-passwd"}

func (e *aria2Engine) readd(ctx context.Context, ref EngineRef, s *aria2.Status, uri string, auth map[string]string) (EngineRef, error) {
	opts := map[string]any{}
	// Keep only what this job set itself: a value equal to the global one
	// came from aria2's defaults and must keep following them. aria2 returns
	// list-valued options like header as arrays, which addUri accepts as is.
	var cur, global map[string]any
	// Without the globals every allowed option is kept, which is still correct.
	_ = e.call(ctx, "aria2.getGlobalOption", &global)
	if e.call(ctx, "aria2.getOption", &cur, string(ref)) == nil {
		for k, v := range cur {
			if !slices.Contains(AllowedOptions, k) || k == "pause" {
				continue
			}
			if slices.Contains(alwaysCopy, k) || !reflect.DeepEqual(v, global[k]) {
				opts[k] = v
			}
		}
	}
	for k, v := range auth {
		opts[k] = v
	}
	opts["dir"] = s.Dir
	derived := false
	if len(s.Files) > 0 && s.Files[0].Path != "" {
		if rel, err := filepath.Rel(s.Dir, s.Files[0].Path); err == nil && !strings.HasPrefix(rel, "..") {
			opts["out"], derived = rel, true
		}
	}
	// The old download wrote nothing, or never learned its file: a file at
	// the name now is not its own, so aria2 renames beside it rather than
	// resuming into it. A partial with its .aria2 still resumes.
	if s.CompletedLength == 0 || !derived {
		opts["continue"] = "false"
	}
	opts["allow-overwrite"] = "false" // see Add
	// A failure here only means the gid is still taken; the add below says so.
	_ = e.call(ctx, "aria2.removeDownloadResult", nil, string(ref))
	opts["gid"] = string(ref)
	var gid string
	err := e.call(ctx, "aria2.addUri", &gid, []string{uri}, opts)
	// Only a refusal from aria2 is safe to retry: after a timeout or a drop
	// the first add may have gone through, and a second would duplicate it.
	var rpcErr *aria2.RPCError
	if err != nil && errors.As(err, &rpcErr) {
		delete(opts, "gid")
		err = e.call(ctx, "aria2.addUri", &gid, []string{uri}, opts)
	}
	if err != nil {
		return "", fmt.Errorf("refresh %s: re-add: %w", ref, err)
	}
	return EngineRef(gid), nil
}
