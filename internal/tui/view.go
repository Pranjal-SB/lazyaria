package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Hit-test layer ids are "<kind>:<id>".
const (
	layerRow  = "row:"
	layerTab  = "tab:"
	layerBtn  = "btn:"
	layerPane = "pane:"
)

// Layer depths. Hit-test layers lie under the drawn screen (z 0), so they
// are never seen; Hit still finds the top-most one with an id.
const (
	zPane = -30 + iota
	zRow
	zDrawn = 0
)

// View draws the screen from the same layers that mouse hit tests use.
func (m Model) View() tea.View {
	defer logPanic(m.o.CrashDir)
	v := tea.NewView(m.compose().Render())
	v.AltScreen = true
	return v
}

// compose lays out the screen: the drawn lines, and blank layers with ids
// over the parts a click can reach.
func (m Model) compose() *lipgloss.Compositor {
	l := m.layout()
	var hits []*lipgloss.Layer
	hitBox := func(id string, x, y, w, h, z int) {
		hits = append(hits, lipgloss.NewLayer(blank(w, h)).ID(id).X(x).Y(y).Z(z))
	}

	tabsLine, rule, spans := m.tabsLine(l.w)
	for i, s := range spans {
		hitBox(layerTab+strconv.Itoa(i), s[0], 2, s[1], 1, zRow)
	}

	rows := m.rows()
	top := m.top[m.tab]
	var body []string
	for k, j := range m.listRows(l, rows, top) {
		body = append(body, j)
		y := l.list.y + 1 + k*l.stride()
		hitBox(layerRow+rows[top+k].ID, l.list.x+1, y, l.list.w-2, l.rowH(), zRow)
	}
	listBody := strings.Join(body, strings.Repeat("\n", l.stride()-l.rowH()+1))
	if len(rows) == 0 {
		listBody = m.emptyView(l.list.w-2, l.list.h-2)
	}
	list := pane(m.theme, tabs[m.tab].name, listBody, l.list.w, l.list.h, true)
	hitBox(layerPane+"list", l.list.x, l.list.y, l.list.w, l.list.h, zPane)
	panes := list
	if d := l.details; d.w > 0 {
		det := pane(m.theme, titleDetails, m.detailsView(l), d.w, d.h, false)
		hitBox(layerPane+"details", d.x, d.y, d.w, d.h, zPane)
		if d.y == l.list.y {
			panes = lipgloss.JoinHorizontal(lipgloss.Top, list, det)
		} else {
			panes = list + "\n" + det
		}
	}

	foot, xs := m.footer(l)
	for i, h := range m.hints() {
		hitBox(layerBtn+h.action, xs[i], l.h-1, lipgloss.Width(h.key)+1+lipgloss.Width(h.label), 1, zRow)
	}

	screen := strings.Join([]string{m.topBar(l.w), "", tabsLine, rule, panes, foot}, "\n")
	return lipgloss.NewCompositor(append(hits, lipgloss.NewLayer(screen).Z(zDrawn))...)
}

// hit is the kind and id of the top-most layer with an id at x, y:
// ("row", job id), ("tab", "2"), ("btn", action), ("pane", "list").
func hit(c *lipgloss.Compositor, x, y int) (kind, id string) {
	kind, id, _ = strings.Cut(c.Hit(x, y).ID(), ":")
	return kind, id
}
