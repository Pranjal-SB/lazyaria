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

// hints are the keys for the selected job, built from what the daemon
// says it allows, plus the ones that always work.
func (m Model) hints() []hint {
	var hs []hint
	if j := m.selected(); j != nil {
		switch {
		case slices.Contains(j.Actions, daemon.ActPause):
			hs = append(hs, hint{keyFor(actToggle), hintPause, actToggle})
		case slices.Contains(j.Actions, daemon.ActResume):
			hs = append(hs, hint{keyFor(actToggle), hintResume, actToggle})
		}
		if m.layout().narrow() && m.o.Settings.UI.DetailsPosition != "hidden" {
			hs = append(hs, hint{keyFor(actDetails), hintDetails, actDetails})
		}
	}
	return append(hs, hint{keyFor(actQuit), hintQuit, actQuit})
}

// footerLeft is the connection error, else the chip that says something
// failed, else the small-terminal hint.
func (m Model) footerLeft(l layout) string {
	t := m.theme
	switch {
	case m.failures >= failuresShown && m.lastErr != nil:
		msg := m.lastErr.Error()
		if !strings.Contains(msg, "doctor") {
			msg = fmt.Sprintf(fmtDoctor, m.lastErr, m.o.Prog)
		}
		return t.bad.Render(msg)
	case m.flash != "":
		return t.bad.Render(m.flash)
	case m.summary.Attention > 0 && m.tab != tabProbIx:
		return t.bad.Render(t.G.icon(store.Error)+" ") + fmt.Sprintf(fmtChipFailed, m.summary.Attention)
	case l.compact:
		return t.muted.Render(textTooSmall)
	}
	return ""
}

// footer lays out the left text and the hints at the right edge. It
// returns each hint's x so clicks can find it.
func (m Model) footer(l layout) (string, []int) {
	t := m.theme
	hs := m.hints()
	parts := make([]string, len(hs))
	for i, h := range hs {
		parts[i] = t.bold.Render(h.key) + " " + t.muted.Render(h.label)
	}
	right := strings.Join(parts, "   ") + " "
	left := " " + m.footerLeft(l)
	room := l.w - lipgloss.Width(right) - 2
	if lipgloss.Width(left) > room {
		left = truncate(left, max(room, 0), m.theme.G.ellipsis)
	}
	xs := make([]int, len(hs))
	x := l.w - lipgloss.Width(right)
	for i, p := range parts {
		xs[i] = x
		x += lipgloss.Width(p) + 3
	}
	return fit(left, l.w-lipgloss.Width(right)) + right, xs
}
