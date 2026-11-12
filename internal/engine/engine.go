// Package engine is the boundary between the job manager and a download
// backend. aria2 is the only backend for now.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Engine interface {
	Start(ctx context.Context) error // spawn or attach; blocks until RPC is ready
	Add(ctx context.Context, req AddRequest) (EngineRef, error)
	Pause(ctx context.Context, ref EngineRef) error
	Resume(ctx context.Context, ref EngineRef) error
	Remove(ctx context.Context, ref EngineRef) error
	ChangeURI(ctx context.Context, ref EngineRef, uri string) (EngineRef, error) // may return a new ref
	Status(ctx context.Context, refs []EngineRef) ([]EngineStatus, error)
	List(ctx context.Context) ([]EngineStatus, error)                  // everything the engine knows, for reconcile
	Details(ctx context.Context, ref EngineRef) (EngineDetails, error) // files, servers/peers
	SetGlobalLimit(ctx context.Context, down, up int64) error
	SetGlobalOptions(ctx context.Context, opts map[string]string) error // aria2 option names, for live settings
	SetLimit(ctx context.Context, ref EngineRef, down, up int64) error
	Events() <-chan EngineEvent // start/pause/stop/complete/error/bt-complete
	Shutdown(ctx context.Context) error
}

// EngineRef identifies a download inside the engine (the aria2 gid).
type EngineRef string

type AddRequest struct {
	URIs       []string
	Torrent    []byte
	Dir        string
	Out        string
	Options    map[string]string
	Paused     bool
	User, Pass string
	// GID asks aria2 to use this gid, so the caller can store the ref before
	// the add. If aria2 refuses it, the download gets a new gid.
	GID string
	// AllowOverwrite lets a re-add restart a partial file that has no
	// control file instead of writing name.1.ext next to it.
	AllowOverwrite bool
	// NoContinue sends continue=false for this download only: a fresh add
	// must not take an existing file without a control file for its own
	// partial download, so aria2 renames beside it instead of writing into it.
	NoContinue bool
}

type EngineStatus struct {
	Ref                                   EngineRef
	State                                 string // active|waiting|paused|error|complete|removed
	Total, Done, Down, Up, Conns, Seeders int64
	ErrorCode                             int
	HTTPStatus                            int
	ErrorMsg                              string
	Path                                  string
	FollowedBy                            EngineRef
	IsBT                                  bool
	Name                                  string
	Known                                 bool // false: the engine has no such download
	Seeder                                bool // a torrent whose payload is done and is now seeding
}

// Event kinds.
const (
	KindStart      = "start"
	KindPause      = "pause"
	KindStop       = "stop"
	KindComplete   = "complete"
	KindError      = "error"
	KindBTComplete = "bt-complete"
	// KindResync has no Ref: events may have been lost, refetch everything.
	KindResync = "resync"
)

type EngineEvent struct {
	Ref  EngineRef
	Kind string // start|pause|stop|complete|error|bt-complete|resync
}

type EngineFile struct {
	Path         string
	Length, Done int64
	Selected     bool
}

type EngineServer struct {
	URI  string
	Down int64
}

type EnginePeer struct {
	IP       string
	Port     int64
	Down, Up int64
	Seeder   bool
}

// EngineDetails is the heavy per-download view: Servers for HTTP/FTP, Peers
// for BitTorrent, and only while the download is active.
type EngineDetails struct {
	Status  EngineStatus
	Files   []EngineFile
	Servers []EngineServer
	Peers   []EnginePeer
}

// ErrInvalidOption is returned for a per-job option outside AllowedOptions.
type ErrInvalidOption struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"` // replaces the list of allowed options
}

func (e ErrInvalidOption) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("option %q: %s", e.Name, e.Reason)
	}
	return fmt.Sprintf("option %q cannot be set per download; allowed: %s", e.Name, strings.Join(AllowedOptions, ", "))
}

// ErrAlreadyComplete is returned by ChangeURI for a finished download.
var ErrAlreadyComplete = errors.New("the download is already complete; there is nothing to refresh")

// ErrNotSupported is returned by ChangeURI for a torrent: refresh is for HTTP and FTP links.
var ErrNotSupported = errors.New("refreshing the link works for HTTP and FTP downloads only, not torrents")

// ErrNotRunning means there is no live connection to aria2 right now.
var ErrNotRunning = errors.New("aria2 engine is not running; run `lazyaria doctor`")
