package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Daemon is what the TUI asks of the daemon; *ipc.Client provides it.
type Daemon interface {
	List(ctx context.Context, includeHistory bool, limit int, ids ...string) (ipc.SnapshotResult, error)
	Subscribe(ctx context.Context, progress bool) (<-chan daemon.Event, <-chan daemon.Frame, error)
	Status(ctx context.Context) (ipc.Status, error)
	Add(ctx context.Context, p daemon.AddParams) (*store.Job, error)
	Pause(ctx context.Context, ids ...string) error
	Resume(ctx context.Context, ids ...string) error
	Remove(ctx context.Context, ids []string, deleteFiles bool) (string, []string, error)
	Undo(ctx context.Context, token string) error
	Details(ctx context.Context, id string, cells int) (daemon.Details, error)
	Close() error
}

var _ Daemon = (*ipc.Client)(nil)

type Options struct {
	// Dial connects and says hello. The TUI reconnects by itself, so the
	// client must not redial (ipc.Client.Redial unset).
	Dial     func(ctx context.Context) (Daemon, error)
	Settings *config.Settings    // effective values, daemon overrides applied
	CrashDir string              // crash-<time>.log goes here
	Prog     string              // the name the binary was invoked as, for hints
	Now      func() time.Time    // nil: time.Now; tests fix it for relative times
	Program  []tea.ProgramOption // tests: input, output, size, colour profile
}

// Run shows the TUI until the user quits. It returns the line to print
// once the terminal is restored, if downloads go on in the background.
func Run(ctx context.Context, o Options) (string, error) {
	opts := append([]tea.ProgramOption{tea.WithContext(ctx)}, o.Program...)
	final, err := tea.NewProgram(New(o), opts...).Run()
	m, ok := final.(Model)
	if ok && m.d != nil {
		m.d.Close()
	}
	if err != nil || !ok {
		return "", err
	}
	return m.note(), nil
}
