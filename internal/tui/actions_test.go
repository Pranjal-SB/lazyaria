package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// actionDaemon serves one running, one paused and one finished job, each on its own tab.
func actionDaemon() *fakeDaemon {
	d := newFakeDaemon(
		job(1, "run.iso", store.Active, daemon.ActPause, daemon.ActRemove),
		job(2, "held.iso", store.Paused, daemon.ActResume, daemon.ActRemove),
		job(3, "old.iso", store.Done, daemon.ActRemove, daemon.ActOpen),
	)
	d.snap.Summary = daemon.Summary{Active: 1, Waiting: 1, Done: 1}
	return d
}

// waitCalls waits until the fake saw want, in order.
func waitCalls(t *testing.T, d *fakeDaemon, want ...string) {
	t.Helper()
	waitFor(t, "calls "+strings.Join(want, ", "), func() bool { return slices.Equal(d.called(), want) })
}

// noCalls checks that nothing reached the fake within a short while.
func noCalls(t *testing.T, d *fakeDaemon) {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	if c := d.called(); len(c) > 0 {
		t.Fatalf("unexpected calls %v", c)
	}
}

func TestSpaceKeyName(t *testing.T) {
	if s := (tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}).String(); s != "space" || actionFor(s) != actToggle {
		t.Fatalf("the space bar is %q, bound to %q", s, actionFor(s))
	}
}

func TestSpaceTogglesByActions(t *testing.T) {
	for _, tc := range []struct {
		tab  string
		want []string
	}{
		{"1", []string{"pause run.iso"}},
		{"2", []string{"resume held.iso"}},
		{"3", nil},
	} {
		d := actionDaemon()
		u := startTUI(t, options(t, d), 120, 32)
		u.waitFor(t, "aria2")
		u.Send(key(tc.tab))
		u.Send(key("space"))
		if tc.want == nil {
			noCalls(t, d)
		} else {
			waitCalls(t, d, tc.want...)
		}
		u.quit(t)
	}
}

func TestRemoveDefaultKeepsFiles(t *testing.T) {
	d := actionDaemon()
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "aria2")
	u.Send(key("x"))
	u.waitFor(t, btnKeep)
	u.Send(key("enter"))
	waitCalls(t, d, "remove run.iso keep")
	u.waitFor(t, "Removed run.iso")
	u.quit(t)
}

func TestRemoveDeleteFiles(t *testing.T) {
	d := actionDaemon()
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "aria2")
	u.Send(tea.KeyPressMsg{Code: tea.KeyDelete})
	u.waitFor(t, btnKeep)
	u.Send(tea.KeyPressMsg{Code: tea.KeyRight})
	u.Send(key("enter"))
	waitCalls(t, d, "remove run.iso trash")
	u.quit(t)
}

func TestRemoveCancel(t *testing.T) {
	d := actionDaemon()
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "aria2")
	u.Send(key("x"))
	u.waitFor(t, btnKeep)
	u.Send(key("esc"))
	noCalls(t, d)
	if m := u.quit(t); m.remove.open() {
		t.Fatal("esc left the dialog open")
	}
}

func TestUndoWithinToast(t *testing.T) {
	d := actionDaemon()
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "aria2")
	u.Send(key("x"))
	u.Send(key("enter"))
	u.waitFor(t, "Removed run.iso")
	u.Send(key("u"))
	waitCalls(t, d, "remove run.iso keep", "undo tok-1")
	u.waitFor(t, "Put back run.iso")
	u.quit(t)
}

func TestUndoAfterToastIgnored(t *testing.T) {
	d := actionDaemon()
	o := options(t, d)
	o.Settings.UI.ToastSeconds = 1
	u := startTUI(t, o, 120, 32)
	u.waitFor(t, "aria2")
	u.Send(key("x"))
	u.Send(key("enter"))
	u.waitFor(t, "Removed run.iso")
	waitFor(t, "the toast to go", func() bool { return !strings.Contains(u.screen(), "Removed run.iso") })
	u.Send(key("u"))
	time.Sleep(200 * time.Millisecond)
	if c := d.called(); !slices.Equal(c, []string{"remove run.iso keep"}) {
		t.Fatalf("calls %v; undo after the toast went must do nothing", c)
	}
	u.quit(t)
}

func TestActionGolden(t *testing.T) {
	for _, tc := range []goldenCase{
		{name: "remove_modal_120x32_ansi", w: 120, h: 32, d: sampleDaemon(), keys: []tea.Msg{key("x")}},
		{name: "help_120x32_ansi", w: 120, h: 32, d: sampleDaemon(), keys: []tea.Msg{key("?")}},
	} {
		tc.profile = colorprofile.ANSI
		t.Run(tc.name, func(t *testing.T) { runGolden(t, tc) })
	}
}

// When the selected job leaves its tab, the next one slides under the
// selection; space and x do nothing to it for a moment, unless the
// person moves the selection themselves.
func TestSelectionMovedGuard(t *testing.T) {
	now := goldenNow
	o := options(t)
	o.Now = func() time.Time { return now }
	a := job(1, "a.iso", store.Active, daemon.ActPause, daemon.ActRemove)
	b := job(2, "b.iso", store.Active, daemon.ActPause, daemon.ActRemove)
	var tm tea.Model = New(o)
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 90, Height: 30}) // no details pane, so no other commands
	tm, _ = tm.Update(connectedMsg{d: newFakeDaemon(), snap: daemon.Snapshot{Seq: 1, Jobs: []*store.Job{a, b}}})
	pausedA := *a
	pausedA.State = store.Paused
	tm, _ = tm.Update(snapshotMsg{snap: daemon.Snapshot{Seq: 2, Jobs: []*store.Job{&pausedA, b}}})
	m := tm.(Model)
	if m.selected().ID != "b.iso" {
		t.Fatalf("selected %s", m.selected().ID)
	}
	if r, cmd := m.Update(key("space")); cmd != nil || r.(Model).remove.open() {
		t.Fatal("space acted on the job that slid under the selection")
	}
	if r, _ := m.Update(key("x")); r.(Model).remove.open() {
		t.Fatal("x opened the dialog for the job that slid under the selection")
	}
	if r, _ := update(m, key("k")).Update(key("x")); !r.(Model).remove.open() {
		t.Fatal("x after moving by hand did nothing")
	}
	now = now.Add(selectionGuard)
	if _, cmd := m.Update(key("space")); cmd == nil {
		t.Fatal("space still did nothing after the guard")
	}
}
