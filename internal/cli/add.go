package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// Add runs `lazyaria add [--dir D] [--out F] [--paused] [--wait] <link|file.torrent|->...`.
func Add(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := newFlags("add", errOut)
	dir := fs.String("dir", "", "download directory")
	outName := fs.String("out", "", "file name to save as")
	paused := fs.Bool("paused", false, "add the jobs paused")
	wait := fs.Bool("wait", false, "wait until every job finishes; exit 1 if any fails")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if *paused && *wait {
		return fail(errOut, "add", usagef("--wait never ends for a paused job; drop --paused or --wait"))
	}
	reqs, err := addRequests(pos)
	if err != nil {
		return fail(errOut, "add", err)
	}
	if *outName != "" && len(reqs) > 1 {
		return fail(errOut, "add", usagef("--out names one file; give one link with it"))
	}
	if *dir != "" {
		// The daemon has its own working directory.
		if *dir, err = filepath.Abs(*dir); err != nil {
			return fail(errOut, "add", err)
		}
	}
	c, err := connect(ctx)
	if err != nil {
		return fail(errOut, "add", err)
	}
	defer c.Close()
	var events <-chan daemon.Event
	if *wait {
		// Subscribe before adding so no change to the new jobs is missed.
		if events, _, err = c.Subscribe(ctx, false); err != nil {
			return fail(errOut, "add", err)
		}
	}

	code, ids := exitOK, map[string]bool{}
	for _, r := range reqs {
		r.Dir, r.Out, r.StartPaused = *dir, *outName, *paused
		j, err := c.Add(ctx, r)
		switch sk, skipped := errors.AsType[*daemon.SkippedError](err); {
		case skipped && j != nil:
			fmt.Fprintf(out, "skipped %s: already in the list as %s (%s)\n", j.Name, j.ID, j.State)
			ids[j.ID] = true
		case skipped:
			fmt.Fprintf(out, "skipped: %s already exists\n", sk.Path)
		case err != nil:
			code = max(code, fail(errOut, "add", addError(err)))
		default:
			fmt.Fprintf(out, "added %s %s\n", j.ID, j.Name)
			ids[j.ID] = true
		}
	}
	if *wait && len(ids) > 0 {
		code = max(code, waitJobs(ctx, c, events, ids, out, errOut))
	}
	return code
}

// addError names the existing job behind a duplicate.
func addError(err error) error {
	if d, ok := errors.AsType[*daemon.DuplicateError](err); ok && d.Existing != nil {
		return fmt.Errorf("%w [existing job %s]", err, d.Existing.ID)
	}
	return err
}

// addRequests turns the arguments into one add per link or torrent file.
// "-" reads links from stdin.
func addRequests(args []string) ([]daemon.AddParams, error) {
	if len(args) == 0 {
		return nil, usagef("give at least one link, .torrent file or - (links on stdin)")
	}
	var links []string
	for _, a := range args {
		if a == "-" {
			b, err := io.ReadAll(stdin)
			if err != nil {
				return nil, err
			}
			found := textutil.ExtractLinks(string(b))
			if len(found) == 0 {
				return nil, usagef("no links found on stdin")
			}
			links = append(links, found...)
			continue
		}
		links = append(links, a)
	}
	var reqs []daemon.AddParams
	for _, l := range links {
		r, err := addRequest(l)
		if err != nil {
			return nil, err
		}
		reqs = append(reqs, r)
	}
	return reqs, nil
}

func addRequest(link string) (daemon.AddParams, error) {
	path := strings.TrimPrefix(link, "file://")
	if strings.HasSuffix(strings.ToLower(path), ".torrent") {
		if b, err := os.ReadFile(path); err == nil {
			return daemon.AddParams{Torrent: b}, nil
		} else if !strings.Contains(link, "://") || strings.HasPrefix(link, "file://") {
			return daemon.AddParams{}, fmt.Errorf("read %s: %w", path, err)
		}
	}
	if !strings.Contains(link, "://") && !strings.HasPrefix(strings.ToLower(link), "magnet:") {
		return daemon.AddParams{}, usagef("%q is not a link or a .torrent file", link)
	}
	return daemon.AddParams{URIs: []string{link}}, nil
}
