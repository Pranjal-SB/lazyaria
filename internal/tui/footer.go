package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// hint is one footer key: the key, what it does, and the action it runs.
type hint struct{ key, label, action string }

// area is a clickable span of one line: a layer id, its x and width.
type area struct {
	id   string
	x, w int
}

// hints are the keys for the selected job, built from what the daemon
// says it allows, plus the ones that always work.
func (m Model) hints() []hint {
	hs := []hint{{keyFor(actAdd), hintAdd, actAdd}}
	if j := m.selected(); j != nil {
		switch {
		case slices.Contains(j.Actions, daemon.ActPause):
			hs = append(hs, hint{keyFor(actToggle), hintPause, actToggle})
		case slices.Contains(j.Actions, daemon.ActResume):
			hs = append(hs, hint{keyFor(actToggle), hintResume, actToggle})
		}
		if slices.Contains(j.Actions, daemon.ActRemove) {
			hs = append(hs, hint{keyFor(actRemove), hintRemove, actRemove})
		}
		if m.layout().narrow() && m.o.Settings.UI.DetailsPosition != "hidden" {
			hs = append(hs, hint{keyFor(actDetails), hintDetails, actDetails})
		}
	}
	return append(hs, hint{keyFor(actHelp), hintHelp, actHelp}, hint{keyFor(actQuit), hintQuit, actQuit})
}

// hintView is a key in bold and its label muted, or bold under the pointer.
func (m Model) hintView(h hint) string {
	label := m.theme.muted.Render(h.label)
	if m.hover == layerBtn+h.action {
		label = m.theme.bold.Render(h.label)
	}
	return m.theme.bold.Render(h.key) + " " + label
}

// footerLeft is the connection error, else the toast, else the chip that
// says something failed, else the small-terminal hint. undoX is where
// the toast's undo key starts, or -1.
func (m Model) footerLeft(l layout) (s string, undoX, undoW int) {
	t := m.theme
	switch {
	case m.failures >= failuresShown && m.lastErr != nil:
		msg := m.lastErr.Error()
		if !strings.Contains(msg, "doctor") {
			msg = fmt.Sprintf(fmtDoctor, m.lastErr, m.o.Prog)
		}
		return t.bad.Render(msg), -1, 0
	case m.toast.text != "":
		return m.toastView()
	case m.summary.Attention > 0 && m.tab != tabProbIx:
		return t.bad.Render(t.G.icon(store.Error)+" ") + fmt.Sprintf(fmtChipFailed, m.summary.Attention), -1, 0
	case l.compact:
		return t.muted.Render(textTooSmall), -1, 0
	}
	return "", -1, 0
}

// footer lays out the left text and the hints at the right edge, and
// says where each clickable key is.
func (m Model) footer(l layout) (string, []area) {
	hs := m.hints()
	parts := make([]string, len(hs))
	for i, h := range hs {
		parts[i] = m.hintView(h)
	}
	right := strings.Join(parts, "   ") + " "
	left, undoX, undoW := m.footerLeft(l)
	left = " " + left
	room := l.w - lipgloss.Width(right) - 2
	if lipgloss.Width(left) > room {
		left = truncate(left, max(room, 0), m.theme.G.ellipsis)
	}
	var areas []area
	if undoX >= 0 && 1+undoX+undoW <= room {
		areas = append(areas, area{layerBtn + actUndo, 1 + undoX, undoW})
	}
	x := max(l.w-lipgloss.Width(right), 0) // keys wider than the screen start at its left edge
	for i, p := range parts {
		areas = append(areas, area{layerBtn + hs[i].action, x, lipgloss.Width(p)})
		x += lipgloss.Width(p) + 3
	}
	return fit(left, l.w-lipgloss.Width(right)) + right, areas
}
