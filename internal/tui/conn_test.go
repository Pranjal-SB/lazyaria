package tui

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func contains(s string) func([]byte) bool {
	return func(b []byte) bool { return bytes.Contains(b, []byte(s)) }
}

// After the daemon goes away the list is never half merged: the TUI says
// it is reconnecting, then shows only the new daemon's jobs.
func TestReconnectResyncs(t *testing.T) {
	d1 := newFakeDaemon(job(1, "alpha.iso", store.Active), job(2, "beta.iso", store.Paused))
	d2 := newFakeDaemon(job(3, "gamma.iso", store.Queued))
	tm := teatest.NewTestModel(t, New(options(t, d1, d2)), teatest.WithInitialTermSize(100, 30))
	teatest.WaitFor(t, tm.Output(), contains("beta.iso"), teatest.WithDuration(5*time.Second))
	close(d1.ev)
	teatest.WaitFor(t, tm.Output(), contains("reconnecting"), teatest.WithDuration(5*time.Second))
	teatest.WaitFor(t, tm.Output(), contains("gamma.iso"), teatest.WithDuration(5*time.Second))
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	v := m.View().Content
	if !strings.Contains(v, "gamma.iso") || strings.Contains(v, "alpha.iso") || strings.Contains(v, "beta.iso") {
		t.Fatalf("after reconnecting the view is:\n%s", v)
	}
	if !strings.Contains(v, iconConnected) || strings.Contains(v, textReconnecting) {
		t.Fatalf("the dot is not back to connected:\n%s", v)
	}
	d1.mu.Lock()
	defer d1.mu.Unlock()
	if !d1.closed {
		t.Fatal("the lost connection was not closed")
	}
}

// Update only returns commands; calling the daemon happens when they run.
func TestUpdateNeverCallsDaemon(t *testing.T) {
	var m tea.Model = New(options(t))
	m, _ = m.Update(connectedMsg{d: panicDaemon{}, snap: daemon.Snapshot{Jobs: []*store.Job{
		job(1, "a.iso", store.Active, daemon.ActPause, daemon.ActRemove),
	}}})
	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeySpace, Text: " "},
		tea.KeyPressMsg{Code: 'x', Text: "x"},
		tea.KeyPressMsg{Code: 'a', Text: "a"},
		eventMsg{ev: daemon.Event{Type: "job.updated"}},
		frameMsg{f: daemon.Frame{Summary: daemon.Summary{Active: 1}}},
		disconnectedMsg{err: io.EOF},
	} {
		m, _ = m.Update(msg)
	}
}

func TestQuitLeavesBackgroundNote(t *testing.T) {
	d := newFakeDaemon(job(1, "a.iso", store.Active))
	d.snap.Summary = daemon.Summary{Active: 2, Waiting: 3, Continuing: 3} // one waiting job is paused
	tm := teatest.NewTestModel(t, New(options(t, d)), teatest.WithInitialTermSize(100, 30))
	teatest.WaitFor(t, tm.Output(), contains("a.iso"), teatest.WithDuration(5*time.Second))
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	if got, want := m.note(), "3 downloads continue in the background; run lazyaria to come back."; got != want {
		t.Fatalf("note %q, want %q", got, want)
	}
	m.summary = daemon.Summary{Active: 1, Waiting: 4, Continuing: 1}
	if got := m.note(); got != "1 download continues in the background; run lazyaria to come back." {
		t.Fatalf("note %q", got)
	}
	m.summary = daemon.Summary{Waiting: 2, Done: 4}
	if got := m.note(); got != "" {
		t.Fatalf("only paused jobs, note %q", got)
	}
}
