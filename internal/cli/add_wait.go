package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// waitJobs blocks until every job in ids is finished: 0 when all are done
// (or seeding, whose payload is complete), 1 when any failed. events must
// have been subscribed before the jobs were added; per the §20 rule the
// snapshot is taken after, and events at or below its seq are dropped.
func waitJobs(ctx context.Context, c *ipc.Client, events <-chan daemon.Event, ids map[string]bool, out, errOut io.Writer) int {
	pending := map[string]bool{}
	for id := range ids {
		pending[id] = true
	}
	code := exitOK
	paused := map[string]*store.Job{} // pending jobs last seen paused
	// settle reports a job once it reaches a final state.
	settle := func(j *store.Job, removed bool) {
		if !pending[j.ID] {
			return
		}
		delete(paused, j.ID)
		if j.State == store.Paused {
			paused[j.ID] = j
		}
		switch {
		case removed || j.State == store.Removed:
			fmt.Fprintf(errOut, "lazyaria add: %s was removed before it finished\n", j.ID)
		case j.State == store.Error:
			msg := ""
			if j.ErrorMsg != nil {
				msg = ": " + *j.ErrorMsg
			}
			fmt.Fprintf(errOut, "lazyaria add: %s %s failed%s\n", j.ID, j.Name, msg)
		case j.State == store.Done || j.State == store.Seeding:
			fmt.Fprintf(out, "done %s %s\n", j.ID, j.Name)
			delete(pending, j.ID)
			return
		default:
			return
		}
		code = exitRuntime
		delete(pending, j.ID)
	}
	// A pause by the daemon itself (no space, disk full) never ends on its
	// own; its banner follows the paused row. A user's pause keeps waiting.
	systemPause := func(data map[string]any) {
		kind, _ := data["kind"].(string)
		for id, j := range paused {
			var why string
			switch {
			case kind == "no_space" && data["job"] == id:
				why = "not enough free space in " + j.Dir
			case kind == "disk_full" && data["dir"] == j.Dir:
				why = "the disk holding " + j.Dir + " is full"
			default:
				continue
			}
			fmt.Fprintf(errOut, "lazyaria add: %s %s was paused: %s; free some space, then run `lazyaria resume %s`\n", id, j.Name, why, id)
			code = exitRuntime
			delete(paused, id)
			delete(pending, id)
		}
	}
	want := make([]string, 0, len(ids))
	for id := range ids {
		want = append(want, id)
	}
	snapshot := func() (uint64, error) {
		r, err := c.List(ctx, true, 0, want...)
		if err != nil {
			return 0, err
		}
		seen := map[string]bool{}
		for _, j := range r.Jobs {
			seen[j.ID] = true
			settle(j, false)
		}
		// Removed rows are not in the snapshot, and the removal event is
		// at or below its seq, so a missing job will never be reported.
		for id := range pending {
			if !seen[id] {
				settle(&store.Job{ID: id}, true)
			}
		}
		return r.Seq, nil
	}

	seq, err := snapshot()
	if err != nil {
		return fail(errOut, "add", err)
	}
	for len(pending) > 0 {
		select {
		case <-ctx.Done():
			return fail(errOut, "add", ctx.Err())
		case ev, ok := <-events:
			if !ok {
				return fail(errOut, "add", fmt.Errorf("lost the daemon while waiting; see `lazyaria logs`"))
			}
			switch {
			case ev.Type == "resync_required":
				if seq, err = snapshot(); err != nil {
					return fail(errOut, "add", err)
				}
			case ev.Type == "banner":
				// Not dropped by seq: the paused row may be in the snapshot.
				systemPause(ev.Data)
			case ev.Seq <= seq || ev.Job == nil:
			default:
				settle(ev.Job, ev.Type == "job.removed")
			}
		}
	}
	return code
}
