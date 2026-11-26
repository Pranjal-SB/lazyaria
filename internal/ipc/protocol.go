// Package ipc is the daemon's JSON-RPC 2.0 interface over a unix socket,
// with server push for job events and progress.
package ipc

import (
	"context"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// MaxFrame is the largest message either side accepts, so a broken or
// hostile peer cannot make the other side allocate without limit.
const MaxFrame = 32 << 20

// Push notification methods.
const (
	PushEvent    = "event"
	PushProgress = "progress"
)

// Handler is what the daemon exposes over the socket. The daemon adapts its
// Manager, store and settings to it.
type Handler interface {
	Hello(ctx context.Context, p HelloParams) (HelloResult, error)
	Status(ctx context.Context) (Status, error)
	Upgrade(ctx context.Context) error
	RestartEngine(ctx context.Context) error

	Add(ctx context.Context, p daemon.AddParams) (*store.Job, error)
	Pause(ctx context.Context, ids []string) error
	Resume(ctx context.Context, ids []string) error
	PauseAll(ctx context.Context) error
	ResumeAll(ctx context.Context) error
	Remove(ctx context.Context, ids []string, deleteFiles bool) (token string, kept []string, err error)
	Undo(ctx context.Context, token string) error
	RefreshURI(ctx context.Context, id, uri string) (*store.Job, error)
	SetLimit(ctx context.Context, id string, down, up int64) error
	StopSeeding(ctx context.Context, ids []string) error
	Snapshot(ctx context.Context, f store.Filter) daemon.Snapshot
	Search(ctx context.Context, query string, limit, offset int) ([]*store.Job, error)
	// Details is one job's files, sources, piece map in cells and recent events.
	Details(ctx context.Context, id string, cells int) (daemon.Details, error)

	// Subscribe and SetObservers back jobs.subscribe; n counts clients
	// that asked for progress.
	Subscribe(progress bool) *daemon.Sub
	SetObservers(n int)
	// SetClients reports how many clients are connected; the daemon stays
	// up while any are.
	SetClients(n int)

	ConfigList(ctx context.Context) ([]config.Entry, error)
	ConfigSet(ctx context.Context, key, value string) error
	ConfigReset(ctx context.Context, key string) error
}

type HelloParams struct {
	Client  string `json:"client"`
	Version string `json:"version"`
}

type HelloResult struct {
	DaemonVersion string `json:"daemon_version"`
	Protocol      int    `json:"protocol"`
	EngineState   string `json:"engine_state"`
}

type Status struct {
	PID             int    `json:"pid"`
	Mode            string `json:"mode"`
	Uptime          int64  `json:"uptime"` // seconds
	EngineState     string `json:"engine_state"`
	Aria2Version    string `json:"aria2_version"`
	DownloadDir     string `json:"download_dir"`
	InhibitingSleep bool   `json:"inhibiting_sleep"`
}

type SnapshotParams struct {
	IncludeHistory bool     `json:"include_history"`
	Limit          int      `json:"limit"`
	IDs            []string `json:"ids,omitempty"` // when set, only these jobs
	// Prefixes, when set, keeps only jobs whose id starts with one of
	// them (ids are upper case). A daemon that predates it sends every job.
	Prefixes []string `json:"prefixes,omitempty"`
	// Nums, when set, keeps only jobs with these numbers. With Prefixes
	// too, a job must match both.
	Nums []int64 `json:"nums,omitempty"`
}

type SnapshotResult = daemon.Snapshot

// SubscribeParams is for jobs.subscribe, which returns {}. To see every
// change exactly once a client subscribes first, then takes jobs.snapshot,
// then drops pushed events whose seq is <= the snapshot's seq.
type SubscribeParams struct {
	Progress bool `json:"progress"`
}

type IDsParams struct {
	IDs []string `json:"ids"`
}

type RemoveParams struct {
	IDs         []string `json:"ids"`
	DeleteFiles bool     `json:"delete_files"`
}

type RemoveResult struct {
	UndoToken string   `json:"undo_token"`
	Kept      []string `json:"kept,omitempty"` // torrent folders kept: their files could not be told apart
}

type UndoParams struct {
	UndoToken string `json:"undo_token"`
}

type RefreshParams struct {
	ID  string `json:"id"`
	URI string `json:"uri"`
}

type LimitParams struct {
	ID   string `json:"id,omitempty"` // empty = global
	Down int64  `json:"down"`
	Up   int64  `json:"up"`
}

type DetailsParams struct {
	ID    string `json:"id"`
	Cells int    `json:"cells"` // piece map width; clamped to 1..512
}

type SearchParams struct {
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type JobResult struct {
	Job *store.Job `json:"job"`
}

type JobsResult struct {
	Jobs []*store.Job `json:"jobs"`
}

type KeyParams struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// ConfigEntry is a config.Entry with its source spelled out.
type ConfigEntry struct {
	Key    string   `json:"key"`
	Value  any      `json:"value"`
	Source string   `json:"source"` // default|file|env|override|flag
	Live   bool     `json:"live"`
	Help   string   `json:"help"`
	Enum   []string `json:"enum,omitempty"`
}

type ConfigListResult struct {
	Entries []ConfigEntry `json:"entries"`
}

func wireEntry(e config.Entry) ConfigEntry {
	return ConfigEntry{Key: e.Key, Value: e.Value, Source: e.Source.String(), Live: e.Live, Help: e.Help, Enum: e.Enum}
}
