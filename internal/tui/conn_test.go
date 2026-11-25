package tui

import (
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// After the daemon goes away the list is never half merged: the TUI says
// it is reconnecting, then shows only the new daemon's jobs.
func TestReconnectResyncs(t *testing.T) {
	d1 := newFakeDaemon(job(1, "alpha.iso", store.Active), job(2, "beta.iso", store.Active))
	d2 := newFakeDaemon(job(3, "gamma.iso", store.Active))
	u := startTUI(t, options(t, d1, d2), 100, 30)
	u.waitFor(t, "beta.iso")
	close(d1.ev)
	u.waitFor(t, "reconnecting")
	u.waitFor(t, "gamma.iso")
	v := u.quit(t).View().Content
	if !strings.Contains(v, "gamma.iso") || strings.Contains(v, "alpha.iso") || strings.Contains(v, "beta.iso") {
		t.Fatalf("after reconnecting the view is:\n%s", v)
	}
	if !strings.Contains(v, unicodeGlyphs.dotOK) || strings.Contains(v, textReconnecting) {
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
	u := startTUI(t, options(t, d), 100, 30)
	u.waitFor(t, "a.iso")
	m := u.quit(t)
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

// Two re-fetches can overlap; an older snapshot never replaces a newer one.
func TestOlderSnapshotDropped(t *testing.T) {
	var m tea.Model = New(options(t))
	m, _ = m.Update(connectedMsg{d: newFakeDaemon(), snap: daemon.Snapshot{Seq: 5}})
	m, _ = m.Update(snapshotMsg{snap: daemon.Snapshot{Seq: 7, Jobs: []*store.Job{job(2, "new.iso", store.Active)}}})
	m, _ = m.Update(snapshotMsg{snap: daemon.Snapshot{Seq: 6, Jobs: []*store.Job{job(1, "old.iso", store.Active)}}})
	if js := m.(Model).jobs; len(js) != 1 || js[0].Name != "new.iso" {
		t.Fatalf("jobs %v after an older snapshot, want only new.iso", js)
	}
}

// The engine dot follows the daemon's engine.state events.
func TestEngineStateFollowsEvents(t *testing.T) {
	m := update(sampleModel(t, 120, 32), eventMsg{ev: daemon.Event{Type: "engine.state", Data: map[string]any{"phase": "ready"}}})
	if !strings.Contains(m.topBar(120), textEngineOK) {
		t.Fatalf("top bar %q before the event", m.topBar(120))
	}
	m = update(m, eventMsg{ev: daemon.Event{Type: "engine.state", Data: map[string]any{"phase": "failed"}}})
	if bar := m.topBar(120); !strings.Contains(bar, "aria2 failed") {
		t.Fatalf("top bar %q after the engine failed", bar)
	}
}
