package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// runAdds runs cmd and the commands it batches, and returns the add
// results among their messages. It runs ticks too, so tests keep them short.
func runAdds(cmd tea.Cmd) (out []tea.Msg) {
	if cmd == nil {
		return nil
	}
	switch v := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range v {
			out = append(out, runAdds(c)...)
		}
	case addMsg:
		out = append(out, v)
	}
	return out
}

func step(m Model, msg tea.Msg) (Model, tea.Cmd) {
	r, c := m.Update(msg)
	return r.(Model), c
}

// batchModel is the sample list at 120x32 on the batch fake, straight
// through Update, so a test decides when each add batch runs.
func batchModel(t *testing.T) (Model, *fakeDaemon) {
	t.Helper()
	d := batchDaemon()
	st, _ := d.Status(context.Background())
	m := update(New(options(t)), tea.WindowSizeMsg{Width: 120, Height: 32}, connectedMsg{d: d, snap: d.snap, status: st})
	return m, d
}

// Typing or pasting while a batch runs is refused, not lost when it ends.
func TestFormLockedWhileBusy(t *testing.T) {
	m, d := batchModel(t)
	m = update(m, tea.PasteMsg{Content: "https://e.example/ok-1"})
	m, c := step(m, key("enter"))
	m = update(m, tea.PasteMsg{Content: " https://e.example/ok-2"}, key("z"))
	if got := m.add.fields[stopLink].Value(); got != "https://e.example/ok-1" {
		t.Fatalf("the field took input while busy: %q", got)
	}
	m = update(m, runAdds(c)...)
	if m.add.open || !slices.Equal(d.called(), []string{addCall("ok-1")}) {
		t.Fatalf("form open %v, calls %q", m.add.open, d.called())
	}
}

// A batch from a form that was closed never asks its question in the
// next form, and does not end that form's own batch.
func TestStaleBatchStaysOut(t *testing.T) {
	m, d := batchModel(t)
	m = update(m, tea.PasteMsg{Content: "https://e.example/dup-1"})
	m, first := step(m, key("enter"))
	m = update(m, key("esc"), key("a"), tea.PasteMsg{Content: "https://e.example/ok-2"})
	m, second := step(m, key("enter"))
	m = update(m, runAdds(first)...)
	if m.add.dup != nil || !m.add.busy {
		t.Fatalf("the old batch reached the new form: dup %v, busy %v", m.add.dup, m.add.busy)
	}
	m, again := step(m, key("enter")) // would be Download again if the question had shown
	m = update(m, runAdds(again)...)
	m = update(m, runAdds(second)...)
	if m.add.open || !strings.Contains(m.toast.text, "ok-2") {
		t.Fatalf("form open %v, toast %q", m.add.open, m.toast.text)
	}
	if c := d.called(); !slices.Equal(c, []string{addCall("dup-1"), addCall("ok-2")}) {
		t.Fatalf("calls %q; the abandoned duplicate must not be forced", c)
	}
}

// Losing the daemon during the duplicate question keeps only the links
// not added yet in the field, and still reports the one that was.
func TestDisconnectAtDuplicate(t *testing.T) {
	m, _ := batchModel(t)
	m = update(m, tea.PasteMsg{Content: "https://e.example/ok-1 https://e.example/dup-1 https://e.example/ok-2"})
	m, c := step(m, key("enter"))
	m = update(m, runAdds(c)...)
	if m.add.dup == nil {
		t.Fatal("no duplicate question")
	}
	m = update(m, disconnectedMsg{gen: m.gen}, key("esc")) // esc skips the duplicate
	if got := m.add.fields[stopLink].Value(); !m.add.open || got != "https://e.example/ok-2" || m.add.msg != textNotConnected {
		t.Fatalf("form open %v, field %q, message %q", m.add.open, got, m.add.msg)
	}
	if !strings.Contains(m.toast.text, "ok-1") {
		t.Fatalf("toast %q does not report the link added before", m.toast.text)
	}
}

// An add that times out may still happen in the daemon, so its link is
// not kept for another try; the person is told to check the list.
func TestAddTimeoutIsUnknown(t *testing.T) {
	addTimeout = 50 * time.Millisecond
	t.Cleanup(func() { addTimeout = callTimeout })
	m, _ := batchModel(t)
	m = update(m, tea.PasteMsg{Content: "https://e.example/slow-1 https://e.example/bad-1"})
	m, c := step(m, key("enter"))
	m = update(m, runAdds(c)...)
	if got := m.add.fields[stopLink].Value(); got != "https://e.example/bad-1" {
		t.Fatalf("field %q; only the refused link belongs there", got)
	}
	if !strings.Contains(m.toast.text, "https://e.example/slow-1 may have been added") {
		t.Fatalf("toast %q", m.toast.text)
	}
}
