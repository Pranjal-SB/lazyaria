package supervisor

import (
	"errors"

	"github.com/Pranjal-SB/lazyaria/internal/config"
)

// SetSettings replaces the settings used by the next launch, so a Restart
// after fixing, say, engine.aria2c_path uses the new value.
func (sv *Supervisor) SetSettings(s *config.Settings) {
	sv.mu.Lock()
	sv.s = s
	sv.mu.Unlock()
}

// Settings returns the settings the next launch will use.
func (sv *Supervisor) Settings() *config.Settings { return sv.settings() }

func (sv *Supervisor) settings() *config.Settings {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return sv.s
}

// Restart leaves the failed state and launches aria2c again with a fresh
// restart policy. The new client arrives on Clients.
func (sv *Supervisor) Restart() error { return sv.RestartWith(nil) }

// RestartWith is Restart with new settings, which take effect only if the
// restart is accepted. A nil s keeps the current settings.
func (sv *Supervisor) RestartWith(s *config.Settings) error {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.ctx.Err() != nil {
		return errors.New("supervisor is shut down; start a new one")
	}
	if !sv.failed {
		return errors.New("aria2c is not in the failed state; nothing to restart")
	}
	if s != nil {
		sv.s = s
	}
	sv.failed = false
	sv.restart <- struct{}{} // buffered, and failed guards against a second send
	return nil
}
