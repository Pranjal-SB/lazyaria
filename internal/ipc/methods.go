package ipc

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

type empty struct{}

// methods maps every protocol method onto the Handler. Errors go through
// toWire and every job that leaves goes through redact.
func (c *conn) methods() handler.Map {
	h := c.s.h
	ok := func(err error) (empty, error) { return empty{}, toWire(err) }
	return handler.Map{
		"hello": handler.New(func(ctx context.Context, p HelloParams) (HelloResult, error) {
			r, err := h.Hello(ctx, p)
			return r, toWire(err)
		}),
		"daemon.status": handler.New(func(ctx context.Context) (Status, error) {
			r, err := h.Status(ctx)
			return r, toWire(err)
		}),
		"daemon.upgrade": handler.New(func(ctx context.Context) (empty, error) { return ok(h.Upgrade(ctx)) }),
		"engine.restart": handler.New(func(ctx context.Context) (empty, error) { return ok(h.RestartEngine(ctx)) }),

		"jobs.snapshot": handler.New(func(ctx context.Context, p SnapshotParams) (SnapshotResult, error) {
			seq, jobs, speed := h.Snapshot(ctx, p.IncludeHistory, p.Limit, p.IDs)
			return SnapshotResult{Seq: seq, Jobs: redactAll(jobs), Speed: speed}, nil
		}),
		"jobs.subscribe": handler.New(func(_ context.Context, p SubscribeParams) (empty, error) {
			c.subscribe(p.Progress)
			return empty{}, nil
		}),
		"jobs.add": handler.New(func(ctx context.Context, p daemon.AddParams) (JobResult, error) {
			j, err := h.Add(ctx, p)
			return JobResult{Job: redact(j)}, toWire(err)
		}),
		"jobs.pause":        handler.New(func(ctx context.Context, p IDsParams) (empty, error) { return ok(h.Pause(ctx, p.IDs)) }),
		"jobs.resume":       handler.New(func(ctx context.Context, p IDsParams) (empty, error) { return ok(h.Resume(ctx, p.IDs)) }),
		"jobs.stop_seeding": handler.New(func(ctx context.Context, p IDsParams) (empty, error) { return ok(h.StopSeeding(ctx, p.IDs)) }),
		"jobs.pause_all":    handler.New(func(ctx context.Context) (empty, error) { return ok(h.PauseAll(ctx)) }),
		"jobs.resume_all":   handler.New(func(ctx context.Context) (empty, error) { return ok(h.ResumeAll(ctx)) }),
		"jobs.remove": handler.New(func(ctx context.Context, p RemoveParams) (RemoveResult, error) {
			tok, err := h.Remove(ctx, p.IDs, p.DeleteFiles)
			return RemoveResult{UndoToken: tok}, toWire(err)
		}),
		"jobs.undo": handler.New(func(ctx context.Context, p UndoParams) (empty, error) { return ok(h.Undo(ctx, p.UndoToken)) }),
		"jobs.refresh_uri": handler.New(func(ctx context.Context, p RefreshParams) (JobResult, error) {
			j, err := h.RefreshURI(ctx, p.ID, p.URI)
			return JobResult{Job: redact(j)}, toWire(err)
		}),
		"jobs.set_limit": handler.New(func(ctx context.Context, p LimitParams) (empty, error) {
			return ok(h.SetLimit(ctx, p.ID, p.Down, p.Up))
		}),
		"jobs.search": handler.New(func(ctx context.Context, p SearchParams) (JobsResult, error) {
			js, err := h.Search(ctx, p.Query, p.Limit, p.Offset)
			return JobsResult{Jobs: redactAll(js)}, toWire(err)
		}),

		"config.list": handler.New(func(ctx context.Context) (ConfigListResult, error) {
			es, err := h.ConfigList(ctx)
			r := ConfigListResult{Entries: make([]ConfigEntry, len(es))}
			for i, e := range es {
				r.Entries[i] = wireEntry(e)
			}
			return r, toWire(err)
		}),
		"config.get": handler.New(func(ctx context.Context, p KeyParams) (ConfigEntry, error) {
			es, err := h.ConfigList(ctx)
			if err != nil {
				return ConfigEntry{}, toWire(err)
			}
			for _, e := range es {
				if e.Key == p.Key {
					return wireEntry(e), nil
				}
			}
			return ConfigEntry{}, jrpc2.Errorf(jrpc2.InvalidParams, "unknown setting %q; see `lazyaria config list`", p.Key)
		}),
		"config.set":   handler.New(func(ctx context.Context, p KeyParams) (empty, error) { return ok(h.ConfigSet(ctx, p.Key, p.Value)) }),
		"config.reset": handler.New(func(ctx context.Context, p KeyParams) (empty, error) { return ok(h.ConfigReset(ctx, p.Key)) }),
	}
}
