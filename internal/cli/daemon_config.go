package cli

import (
	"context"
	"errors"
	"maps"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
)

// load reads every config layer, the DB overrides included.
func (d *daemonProc) load() (*config.Settings, []config.Entry, error) {
	s, ents, warns, err := config.Load(d.paths.ConfigFile, d.env, d.st, d.flags)
	if err != nil {
		return nil, nil, err
	}
	for _, w := range warns {
		d.log.Warn("config: "+w.Msg, "key", w.Key, "line", w.Line)
	}
	if s.Downloads.Dir == "" {
		s.Downloads.Dir = d.paths.DownloadDir
	}
	return s, ents, nil
}

// reload re-reads the config and applies the live keys at once. The rest
// waits for a daemon restart.
func (d *daemonProc) reload(ctx context.Context) error {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	next, ents, err := d.load()
	if err != nil {
		return err
	}
	prev := d.cfg.Load()
	cur := config.WithLive(prev, next)
	d.cfg.Store(cur)
	d.entries.Store(&ents)
	d.m.SetSettings(cur)
	if d.sv != nil {
		// Onto the supervisor's own settings: engine.restart may have given
		// it restart-only keys newer than the daemon's.
		d.sv.SetSettings(config.WithLive(d.sv.Settings(), next))
	}
	d.level.Set(logLevel(cur.Log.Level))
	d.redact.Store(cur.Privacy.RedactLogs)
	d.kick()
	err = d.applyEngine(ctx, prev, cur)
	// The new settings are in place even when aria2 refused some, so
	// clients re-read them either way.
	d.m.ConfigChanged()
	return err
}

// applyEngine pushes changed live aria2 options to the running aria2.
func (d *daemonProc) applyEngine(ctx context.Context, prev, cur *config.Settings) error {
	opts := supervisor.ChangedOptions(prev, cur, d.enginePaths(cur))
	var err error
	if prev.Limits != cur.Limits {
		down, _ := config.ParseSize(cur.Limits.GlobalDown) // validated on load
		up, _ := config.ParseSize(cur.Limits.GlobalUp)
		err = d.eng.SetGlobalLimit(ctx, down, up)
	}
	maps.DeleteFunc(opts, func(k, _ string) bool {
		return k == "max-overall-download-limit" || k == "max-overall-upload-limit"
	})
	if len(opts) > 0 && err == nil {
		err = d.eng.SetGlobalOptions(ctx, opts)
	}
	if errors.Is(err, engine.ErrNotRunning) {
		// aria2 is down; it reads the new values from its conf on restart.
		d.log.Info("aria2 not running; live settings apply when it restarts")
		return nil
	}
	return err
}

// setSetting validates and stores a runtime override, then applies it if live.
func (d *daemonProc) setSetting(ctx context.Context, key, value string) error {
	scratch := *d.cfg.Load()
	scratch.Keys, scratch.Theme = maps.Clone(scratch.Keys), maps.Clone(scratch.Theme)
	if err := config.Set(&scratch, key, value); err != nil {
		return err
	}
	if err := d.st.SetSetting(context.WithoutCancel(ctx), key, value); err != nil {
		return err
	}
	return d.reload(ctx)
}

// resetSetting drops a runtime override.
func (d *daemonProc) resetSetting(ctx context.Context, key string) error {
	if err := d.st.DeleteSetting(context.WithoutCancel(ctx), key); err != nil {
		return err
	}
	return d.reload(ctx)
}

// restartEngine retries a failed aria2 with a freshly read config, so a
// corrected engine.aria2c_path takes effect.
func (d *daemonProc) restartEngine() error {
	if d.sv == nil {
		return errNoEngineRestart
	}
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	next, _, err := d.load()
	if err != nil {
		return err
	}
	return d.sv.RestartWith(next)
}
