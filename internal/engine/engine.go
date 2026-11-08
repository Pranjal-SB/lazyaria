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
	Details(ctx context.Context, ref EngineRef) (EngineDetails, error) // files, servers/peers
	SetGlobalLimit(ctx context.Context, down, up int64) error
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
type ErrInvalidOption struct{ Name string }

func (e ErrInvalidOption) Error() string {
	return fmt.Sprintf("option %q cannot be set per download; allowed: %s", e.Name, strings.Join(AllowedOptions, ", "))
}

// ErrNotRunning means there is no live connection to aria2 right now.
var ErrNotRunning = errors.New("aria2 engine is not running; run `lazyaria doctor`")
