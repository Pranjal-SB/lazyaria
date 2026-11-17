package cli

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/buildinfo"
	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// handler adapts the daemon to ipc.Handler. Job methods come straight
// from the Manager.
type handler struct {
	*daemon.Manager
	d *daemonProc
}

var _ ipc.Handler = handler{}

func (h handler) Hello(context.Context, ipc.HelloParams) (ipc.HelloResult, error) {
	return ipc.HelloResult{DaemonVersion: buildinfo.Version, Protocol: buildinfo.Protocol, EngineState: h.d.engineState()}, nil
}

func (h handler) Status(context.Context) (ipc.Status, error) {
	s := h.d.cfg.Load()
	return ipc.Status{
		PID:             os.Getpid(),
		Mode:            s.Daemon.Mode,
		Uptime:          int64(time.Since(h.d.started) / time.Second),
		EngineState:     h.d.engineState(),
		Aria2Version:    h.d.ariaVer,
		DownloadDir:     s.Downloads.Dir,
		InhibitingSleep: h.d.inhibiting.Load(),
	}, nil
}

// Upgrade only queues the upgrade, so this call's reply goes out first.
func (h handler) Upgrade(ctx context.Context) error {
	if err := checkUpgrade(ctx); err != nil {
		h.d.log.Warn("upgrade refused", "err", err)
		return err
	}
	select {
	case h.d.upgradeReq <- struct{}{}:
	default:
	}
	return nil
}

func (h handler) RestartEngine(context.Context) error { return h.d.restartEngine() }

func (h handler) Search(ctx context.Context, q string, limit, offset int) ([]*store.Job, error) {
	return h.d.st.Search(ctx, q, limit, offset)
}

func (h handler) Subscribe(progress bool) *daemon.Sub { return h.d.bus.Subscribe(progress) }

func (h handler) SetClients(n int) {
	h.d.clients.Store(int64(n))
	h.d.kick()
}

// SetLimit with no id sets the global limits as runtime settings, the way
// config set limits.global_down does, so they outlive an aria2 restart.
func (h handler) SetLimit(ctx context.Context, id string, down, up int64) error {
	if id != "" || down < 0 || up < 0 {
		return h.Manager.SetLimit(ctx, id, down, up) // also rejects negatives
	}
	if err := h.d.setSetting(ctx, "limits.global_down", strconv.FormatInt(down, 10)); err != nil {
		return err
	}
	return h.d.setSetting(ctx, "limits.global_up", strconv.FormatInt(up, 10))
}

func (h handler) ConfigList(context.Context) ([]config.Entry, error) { return *h.d.entries.Load(), nil }

func (h handler) ConfigSet(ctx context.Context, key, value string) error {
	return h.d.setSetting(ctx, key, value)
}

func (h handler) ConfigReset(ctx context.Context, key string) error {
	return h.d.resetSetting(ctx, key)
}
