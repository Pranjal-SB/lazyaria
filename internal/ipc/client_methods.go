package ipc

import (
	"context"
	"errors"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Add queues a download. A *daemon.SkippedError comes back together with
// the existing job; callers treat it as success.
func (c *Client) Add(ctx context.Context, p daemon.AddParams) (*store.Job, error) {
	var r JobResult
	err := c.call(ctx, "jobs.add", p, &r)
	if sk, ok := errors.AsType[*daemon.SkippedError](err); ok {
		return sk.Existing, sk
	}
	return r.Job, err
}

// List returns the daemon's job snapshot; given ids, only those jobs.
func (c *Client) List(ctx context.Context, includeHistory bool, limit int, ids ...string) (SnapshotResult, error) {
	var r SnapshotResult
	err := c.call(ctx, "jobs.snapshot", SnapshotParams{IncludeHistory: includeHistory, Limit: limit, IDs: ids}, &r)
	return r, err
}

// Search matches query against the whole history, server side.
func (c *Client) Search(ctx context.Context, query string, limit, offset int) ([]*store.Job, error) {
	var r JobsResult
	err := c.call(ctx, "jobs.search", SearchParams{Query: query, Limit: limit, Offset: offset}, &r)
	return r.Jobs, err
}

func (c *Client) Pause(ctx context.Context, ids ...string) error {
	return c.call(ctx, "jobs.pause", IDsParams{IDs: ids}, nil)
}

func (c *Client) Resume(ctx context.Context, ids ...string) error {
	return c.call(ctx, "jobs.resume", IDsParams{IDs: ids}, nil)
}

func (c *Client) PauseAll(ctx context.Context) error { return c.call(ctx, "jobs.pause_all", nil, nil) }
func (c *Client) ResumeAll(ctx context.Context) error {
	return c.call(ctx, "jobs.resume_all", nil, nil)
}

func (c *Client) StopSeeding(ctx context.Context, ids ...string) error {
	return c.call(ctx, "jobs.stop_seeding", IDsParams{IDs: ids}, nil)
}

// Remove returns an undo token valid for a short while, and the torrent
// folders it kept (see RemoveResult).
func (c *Client) Remove(ctx context.Context, ids []string, deleteFiles bool) (string, []string, error) {
	var r RemoveResult
	err := c.call(ctx, "jobs.remove", RemoveParams{IDs: ids, DeleteFiles: deleteFiles}, &r)
	return r.UndoToken, r.Kept, err
}

func (c *Client) Undo(ctx context.Context, token string) error {
	return c.call(ctx, "jobs.undo", UndoParams{UndoToken: token}, nil)
}

// Refresh gives a job a fresh link and keeps its progress.
func (c *Client) Refresh(ctx context.Context, id, uri string) (*store.Job, error) {
	var r JobResult
	err := c.call(ctx, "jobs.refresh_uri", RefreshParams{ID: id, URI: uri}, &r)
	return r.Job, err
}

// SetLimit sets a job's speed limits, or the global ones when id is empty.
func (c *Client) SetLimit(ctx context.Context, id string, down, up int64) error {
	return c.call(ctx, "jobs.set_limit", LimitParams{ID: id, Down: down, Up: up}, nil)
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var r Status
	err := c.call(ctx, "daemon.status", nil, &r)
	return r, err
}

func (c *Client) Upgrade(ctx context.Context) error { return c.call(ctx, "daemon.upgrade", nil, nil) }
func (c *Client) RestartEngine(ctx context.Context) error {
	return c.call(ctx, "engine.restart", nil, nil)
}

func (c *Client) ConfigList(ctx context.Context) ([]ConfigEntry, error) {
	var r ConfigListResult
	err := c.call(ctx, "config.list", nil, &r)
	return r.Entries, err
}

func (c *Client) ConfigGet(ctx context.Context, key string) (ConfigEntry, error) {
	var r ConfigEntry
	err := c.call(ctx, "config.get", KeyParams{Key: key}, &r)
	return r, err
}

func (c *Client) ConfigSet(ctx context.Context, key, value string) error {
	return c.call(ctx, "config.set", KeyParams{Key: key, Value: value}, nil)
}

func (c *Client) ConfigReset(ctx context.Context, key string) error {
	return c.call(ctx, "config.reset", KeyParams{Key: key}, nil)
}
