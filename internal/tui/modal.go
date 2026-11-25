package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The remove dialog's buttons, as their hit-test ids.
const (
	choiceKeep   = "keep"
	choiceTrash  = "trash"
	choiceCancel = "cancel"
)

var removeChoices = []string{choiceKeep, choiceTrash, choiceCancel}

// removeModal asks how to remove a job. The first button, which keeps the
// files, has focus, so enter never deletes anything.
type removeModal struct {
	ids   []string
	name  string
	focus int
}

func (r removeModal) open() bool { return len(r.ids) > 0 }

func (m Model) removeKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	n := len(removeChoices)
	switch k.String() {
	case "left", "shift+tab", "h":
		m.remove.focus = (m.remove.focus + n - 1) % n
	case "right", "tab", "l":
		m.remove.focus = (m.remove.focus + 1) % n
	case "enter":
		return m.press(removeChoices[m.remove.focus])
	case "esc":
		m.remove = removeModal{}
	}
	return m, nil
}

// press closes the dialog and does what the button says.
func (m Model) press(choice string) (Model, tea.Cmd) {
	r := m.remove
	m.remove = removeModal{}
	switch choice {
	case choiceKeep:
		return m, m.removeJobs(r.ids, r.name, false)
	case choiceTrash:
		return m, m.removeJobs(r.ids, r.name, true)
	}
	return m, nil
}

// overlay is a box drawn over the screen, with its clickable parts
// relative to its top-left corner.
type overlay struct {
	body string
	hits []hitArea
}

type hitArea struct {
	id         string
	x, y, w, h int
}

// removeView is the remove dialog: the question and three buttons.
func (m Model) removeView(maxW int) overlay {
	t := m.theme
	labels := []string{btnKeep, btnTrash, btnCancel}
	var btns []string
	var hits []hitArea
	x := 2 + 1 // border, then padding
	for i, label := range labels {
		st := lipgloss.NewStyle()
		switch {
		case i == m.remove.focus:
			st = t.accent.Bold(true).Reverse(true)
		case removeChoices[i] == choiceTrash:
			st = t.bad
		}
		if m.hover == layerModal+removeChoices[i] {
			st = st.Bold(true)
		}
		b := st.Render("[ " + label + " ]")
		btns = append(btns, b)
		hits = append(hits, hitArea{layerModal + removeChoices[i], x, 4, lipgloss.Width(b), 1})
		x += lipgloss.Width(b) + 2
	}
	row := strings.Join(btns, "  ")
	w := min(lipgloss.Width(row)+6, maxW)
	q := truncate(fmt.Sprintf(fmtRemoveQ, m.remove.name), w-6, t.G.ellipsis)
	body := strings.Join([]string{"", "  " + q, "", "  " + row, "", "  " + t.muted.Render(textRemoveKeys)}, "\n")
	// buttons cut off by a narrow terminal cannot be clicked
	hits = slices.DeleteFunc(hits, func(h hitArea) bool { return h.x+h.w > w-1 })
	return overlay{pane(t, titleRemove, body, w, 8, true), hits}
}
