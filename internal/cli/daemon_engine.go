package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
)

// startEngine supervises a new aria2c, takes over the one a previous image
// handed down, or attaches to engine.attach_url. A supervised aria2 that
// fails is not fatal: the daemon keeps serving so clients can see why and
// ask for engine.restart.
func (d *daemonProc) startEngine(ctx context.Context, s *config.Settings) error {
	if s.Engine.AttachURL != "" {
		secret, err := attachSecret(s)
		if err != nil {
			return err
		}
		d.eng = engine.NewAria2Attach(s.Engine.AttachURL, secret)
		if err := d.eng.Start(ctx); err != nil {
			return fmt.Errorf("attach to aria2 at %s: %w; check engine.attach_url and the secret, or run `lazyaria doctor`", s.Engine.AttachURL, err)
		}
		d.engState.Store(supervisor.PhaseReady)
		return nil
	}
	d.sv = supervisor.New(s, d.enginePaths(s), d.log)
	if d.handoff != nil && d.handoff.Aria2 != nil {
		d.sv.Adopt(*d.handoff.Aria2)
	}
	go d.followEngine(d.sv.State())
	d.eng = engine.NewAria2(d.sv)
	if err := d.eng.Start(ctx); err != nil {
		if ctx.Err() != nil {
			return err
		}
		d.log.Error("aria2 did not start; serving anyway, fix it and run `lazyaria doctor --restart-engine`", "err", err)
	}
	return nil
}

// enginePaths points aria2's default dir at the configured download dir.
func (d *daemonProc) enginePaths(s *config.Settings) config.Paths {
	p := d.paths
	p.DownloadDir = s.Downloads.Dir
	return p
}

func attachSecret(s *config.Settings) (string, error) {
	if v := os.Getenv("LAZYARIA_ARIA2_SECRET"); v != "" {
		return v, nil
	}
	if s.Engine.AttachSecretFile == "" {
		return "", nil
	}
	b, err := os.ReadFile(s.Engine.AttachSecretFile)
	if err != nil {
		return "", fmt.Errorf("read engine.attach_secret_file: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// followEngine publishes every supervisor phase change to clients.
func (d *daemonProc) followEngine(states <-chan supervisor.EngineState) {
	for st := range states {
		d.engState.Store(st.Phase)
		data := map[string]any{"phase": st.Phase}
		if len(st.LogTail) > 0 {
			data["log_tail"] = st.LogTail
		}
		d.bus.Publish(daemon.Event{Type: "engine.state", Data: data})
	}
}

func (d *daemonProc) engineState() string {
	if s, _ := d.engState.Load().(string); s != "" {
		return s
	}
	return supervisor.PhaseStarting
}

var errNoEngineRestart = errors.New("lazyaria is attached to an aria2 it does not run (engine.attach_url); restart that aria2 instead")
