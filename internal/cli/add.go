package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/addlink"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// Add runs `lazyaria add [--dir D] [--out F] [--paused] [--wait] [--force] <link|file.torrent|->...`.
func Add(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := newFlags("add", errOut)
	dir := fs.String("dir", "", "download directory")
	outName := fs.String("out", "", "file name to save as")
	paused := fs.Bool("paused", false, "add the jobs paused")
	wait := fs.Bool("wait", false, "wait until every job finishes; exit 1 if any fails")
	force := fs.Bool("force", false, "add even if the link is already in the list")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if *paused && *wait {
		return fail(errOut, "add", usagef("--wait never ends for a paused job; drop --paused or --wait"))
	}
	reqs, links, err := addRequests(pos)
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

	code, ids := exitOK, map[string]*store.Job{}
	for i, r := range reqs {
		r.Dir, r.Out, r.StartPaused, r.Force = *dir, *outName, *paused, *force
		j, err := c.Add(ctx, r)
		if errors.Is(err, ipc.ErrCutOff) {
			err = fmt.Errorf("%s: %w", links[i], err)
		}
		switch sk, skipped := errors.AsType[*daemon.SkippedError](err); {
		case *wait && errors.Is(err, ipc.ErrCutOff):
			// Subscribed, the client cannot reconnect; nothing more goes out.
			code = max(code, fail(errOut, "add", err))
			if rest := links[i+1:]; len(rest) > 0 {
				fmt.Fprintf(errOut, "lazyaria add: not sent, add them again: %s\n", strings.Join(rest, " "))
			}
			return code
		case errors.Is(err, ipc.ErrNotSent):
			fmt.Fprintf(errOut, "lazyaria add: not sent, add them again: %s\n", strings.Join(links[i:], " "))
			return max(code, exitRuntime)
		case skipped && j != nil:
			fmt.Fprintf(out, "skipped %s: already in the list as %s (%s)\n", j.Name, numRef(j), j.State)
			ids[j.ID] = j
		case skipped:
			fmt.Fprintf(out, "skipped: %s already exists\n", sk.Path)
		case err != nil:
			code = max(code, fail(errOut, "add", addError(err)))
		default:
			fmt.Fprintf(out, "added %s %s\n", numRef(j), j.Name)
			ids[j.ID] = j
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
		return fmt.Errorf("%w [existing job %s]", err, numRef(d.Existing))
	}
	return err
}

// addRequests turns the arguments into one add per link or torrent file,
// and returns the links (or file names) in the same order. "-" reads links
// from stdin.
func addRequests(args []string) ([]daemon.AddParams, []string, error) {
	if len(args) == 0 {
		return nil, nil, usagef("give at least one link, .torrent file or - (links on stdin)")
	}
	var links []string
	for _, a := range args {
		if a == "-" {
			b, err := io.ReadAll(stdin)
			if err != nil {
				return nil, nil, err
			}
			found := textutil.ExtractLinks(string(b))
			if len(found) == 0 {
				return nil, nil, usagef("no links found on stdin")
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
			return nil, nil, err
		}
		reqs = append(reqs, r)
	}
	return reqs, links, nil
}

func addRequest(link string) (daemon.AddParams, error) {
	p, err := addlink.Params(link)
	if errors.Is(err, addlink.ErrNotLink) {
		return p, usagef("%q is not a link or a .torrent file", link)
	}
	return p, err
}
