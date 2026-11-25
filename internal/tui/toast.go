package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// toast is a short note in the footer that goes away by itself after
// ui.toast_seconds. A remove's toast holds the undo token for u.
type toast struct {
	text       string
	bad        bool
	undo, name string
	seq        int
}

type toastDoneMsg struct{ seq int }

func (m *Model) showToast(t toast) tea.Cmd {
	m.toastSeq++
	t.seq = m.toastSeq
	m.toast = t
	return tea.Tick(time.Duration(m.o.Settings.UI.ToastSeconds)*time.Second, func(time.Time) tea.Msg { return toastDoneMsg{t.seq} })
}

// toastView is the toast in the footer, and where its undo key sits.
func (m Model) toastView() (s string, undoX, undoW int) {
	t := m.theme
	if m.toast.bad {
		return t.bad.Render(m.toast.text), -1, 0
	}
	s = m.toast.text
	if m.toast.undo == "" {
		return s, -1, 0
	}
	h := hint{keyFor(actUndo), hintUndo, actUndo}
	hv := m.hintView(h)
	return s + "  " + hv, lipgloss.Width(s) + 2, lipgloss.Width(hv)
}
