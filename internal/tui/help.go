package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// helpView lists every key from the key table.
func (m Model) helpView(maxW, maxH int) overlay {
	t := m.theme
	var lines []string
	kw := 0
	for _, d := range keyTable {
		kw = max(kw, lipgloss.Width(strings.Join(d.keys, " ")))
	}
	for _, d := range keyTable {
		lines = append(lines, " "+t.bold.Render(fmt.Sprintf("%-*s", kw, strings.Join(d.keys, " ")))+"  "+d.help)
	}
	lines = append(lines, "", " "+t.muted.Render(textShiftSelect))
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	w = min(w+3, maxW)
	h := min(len(lines)+2, maxH)
	return overlay{body: pane(t, titleHelp, strings.Join(lines, "\n"), w, h, true)}
}
