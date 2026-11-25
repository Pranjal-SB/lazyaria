package tui

import (
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// mouseMode is the reporting ui.mouse asks for: all motion gives hover.
func (m Model) mouseMode() tea.MouseMode {
	switch m.o.Settings.UI.Mouse {
	case "all":
		return tea.MouseModeAllMotion
	case "cell":
		return tea.MouseModeCellMotion
	}
	return tea.MouseModeNone
}

// mouse finds what is under the pointer in the layers the screen was
// drawn from, and acts on it. Every mouse action also has a key.
func (m Model) mouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	ms := msg.Mouse()
	kind, id := hit(m.compose(), ms.X, ms.Y)
	switch msg.(type) {
	case tea.MouseMotionMsg:
		m.hover = ""
		if id != "" && kind != "pane" {
			m.hover = kind + ":" + id
		}
	case tea.MouseWheelMsg:
		delta := 0
		switch ms.Button {
		case tea.MouseWheelDown:
			delta = 1
		case tea.MouseWheelUp:
			delta = -1
		}
		switch {
		case m.remove.open() || m.help || m.add.open:
		case kind == "pane" && id == "details":
			m.detTop = min(max(m.detTop+delta, 0), m.detailsMaxTop())
		case kind == "row" || kind == "pane" && id == "list":
			m.move(delta)
		}
	case tea.MouseClickMsg:
		if ms.Button != tea.MouseLeft {
			return m, nil
		}
		return m.click(kind, id)
	}
	return m, nil
}

func (m Model) click(kind, id string) (Model, tea.Cmd) {
	switch {
	case m.help:
		m.help = false
	case m.add.open:
		switch {
		case kind != "modal":
			m.add = addForm{}
		case id != "":
			return m.formClick(id)
		}
	case m.remove.open():
		switch {
		case kind != "modal":
			m.remove = removeModal{}
		case id != "":
			return m.press(id)
		}
	case kind == "row":
		if i := slices.IndexFunc(m.rows(), func(j *store.Job) bool { return j.ID == id }); i >= 0 {
			m.sel[m.tab], m.selIx[m.tab] = id, i
		}
	case kind == "tab":
		if i, err := strconv.Atoi(id); err == nil && i < m.tabsShown() {
			m.tab = i
		}
	case kind == "btn":
		return m.run(id)
	}
	return m, nil
}

// detailsMaxTop is how far the details pane can scroll.
func (m Model) detailsMaxTop() int {
	l := m.layout()
	if l.details.w == 0 {
		return 0
	}
	return max(strings.Count(m.detailsText(l), "\n")+1-(l.details.h-2), 0)
}
