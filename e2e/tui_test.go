//go:build e2e && linux

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
	"github.com/Pranjal-SB/lazyaria/internal/tui"
)

// screen wraps the TUI model to keep the last screen it drew; the
// program's output only carries the cells that changed.
type screen struct {
	tea.Model
	last *atomic.Pointer[string]
}

func (s screen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := s.Model.Update(msg)
	return screen{m, s.last}, cmd
}

func (s screen) View() tea.View {
	v := s.Model.View()
	s.last.Store(&v.Content)
	return v
}

// TestTUISmoke drives the terminal interface against a real daemon: paste
// a link, add it, watch it finish, remove it with the dialog's default
// and quit. The file stays; the job is gone.
func TestTUISmoke(t *testing.T) {
	e := newEnv(t, "")
	srv := testutil.NewFaultServer(t, map[string][]byte{"tui.bin": payload(2<<20, 0x71)})
	link := srv.URL("tui.bin", testutil.ModeOK)
	e.ok("status") // starts the daemon

	s, _, _, err := config.Load("", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := tui.Options{
		Settings: s,
		CrashDir: t.TempDir(),
		Prog:     "lazyaria",
		Dial: func(ctx context.Context) (tui.Daemon, error) {
			c, err := ipc.Connect(ctx, e.p, false, "")
			if err != nil {
				return nil, err
			}
			if _, _, err := c.Hello(ctx, false); err != nil {
				c.Close()
				return nil, err
			}
			return c, nil
		},
	}
	last := &atomic.Pointer[string]{}
	tm := teatest.NewTestModel(t, screen{tui.New(o), last}, teatest.WithInitialTermSize(120, 32))
	shown := func() string {
		if p := last.Load(); p != nil {
			return *p
		}
		return ""
	}
	wait := func(d time.Duration, want ...string) {
		t.Helper()
		waitFor(t, d, strings.Join(want, " and ")+" on screen", func() bool {
			for _, w := range want {
				if !strings.Contains(shown(), w) {
					return false
				}
			}
			return true
		})
	}

	wait(10*time.Second, "aria2 ok")
	tm.Send(tea.PasteMsg{Content: "get this: " + link})
	wait(5*time.Second, "Add a download", link)
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	wait(10*time.Second, "Added #1 tui.bin")
	wait(60*time.Second, "Finished 1")
	tm.Send(tea.KeyPressMsg{Code: '3', Text: "3"})
	wait(5*time.Second, "✓", "tui.bin", "done")
	tm.Send(tea.KeyPressMsg{Code: 'x', Text: "x"})
	wait(5*time.Second, "Remove tui.bin?")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	wait(10*time.Second, "Finished downloads show up here.")
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	if _, err := os.Stat(filepath.Join(e.dl, "tui.bin")); err != nil {
		t.Fatalf("remove from the list took the file: %v", err)
	}
	if slices.ContainsFunc(e.jobs(), func(j *store.Job) bool { return j.Name == "tui.bin" && j.State != store.Removed }) {
		t.Fatal("the removed job is still in the list")
	}
}
