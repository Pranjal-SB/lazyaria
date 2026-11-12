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
	// settle reports a job once it reaches a final state.
	settle := func(j *store.Job, removed bool) {
		if !pending[j.ID] {
			return
		}
		switch {
		case removed || j.State == store.Removed:
			fmt.Fprintf(errOut, "lazyaria add: %s %s was removed\n", j.ID, j.Name)
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
	snapshot := func() (uint64, error) {
		r, err := c.List(ctx, true, 0)
		for _, j := range r.Jobs {
			settle(j, false)
		}
		return r.Seq, err
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
			case ev.Seq <= seq || ev.Job == nil:
			default:
				settle(ev.Job, ev.Type == "job.removed")
			}
		}
	}
	return code
}
