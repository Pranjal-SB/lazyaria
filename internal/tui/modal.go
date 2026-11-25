package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Pranjal-SB/lazyaria/internal/tui/form"
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
	ids  []string
	name string
	btns form.Buttons
	// A dialog opened by a click ignores clicks on its buttons until the
	// pointer leaves that cell, so the second click of a double click
	// cannot press the button that appeared under it.
	armed  bool
	openAt [2]int
}

func newRemoveModal(t Theme, id, name string) removeModal {
	return removeModal{ids: []string{id}, name: name, armed: true,
		btns: form.Buttons{Labels: []string{btnKeep, fmt.Sprintf(fmtBtnTrash, t.G.arrow), btnCancel}}}
}

func (r removeModal) open() bool { return len(r.ids) > 0 }

func (m Model) removeKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	switch k.String() {
	case "left", "shift+tab", "h":
		m.remove.btns.Move(-1)
	case "right", "tab", "l":
		m.remove.btns.Move(1)
	case "enter":
		return m.press(removeChoices[m.remove.btns.Focus])
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
	row, spans := m.remove.btns.View(func(i int, focused bool) lipgloss.Style {
		st := lipgloss.NewStyle()
		switch {
		case focused:
			st = t.accent.Bold(true).Reverse(true)
		case removeChoices[i] == choiceTrash:
			st = t.bad
		}
		if m.hover == layerModal+removeChoices[i] {
			st = st.Bold(true)
		}
		return st
	})
	var hits []hitArea
	for i, sp := range spans {
		hits = append(hits, hitArea{layerModal + removeChoices[i], 3 + sp[0], 4, sp[1], 1}) // border and padding, fourth line
	}
	w := max(min(lipgloss.Width(row)+6, maxW), 0)
	q := truncate(fmt.Sprintf(fmtRemoveQ, m.remove.name), w-6, t.G.ellipsis)
	body := strings.Join([]string{"", "  " + q, "", "  " + row, "", "  " + t.muted.Render(textRemoveKeys)}, "\n")
	return overlay{pane(t, titleRemove, body, w, 8, true), clipHits(hits, w)}
}
