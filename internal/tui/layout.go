package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	topRows    = 4 // top bar, a blank line, the tabs and their rule
	footerRows = 1
	minWidth   = 80 // below 80x24 rows are compact and details hidden
	minHeight  = 24
	bottomRows = 8 // the details pane under the list on narrow layouts
	defaultW   = 80
	defaultH   = 24
	rowStride  = 3 // a job takes two lines, plus a blank one between jobs
	rowLines   = 2
)

type rect struct{ x, y, w, h int }

// layout is where everything goes at the current size and settings.
type layout struct {
	w, h    int
	compact bool // one-line rows, no details
	wide    bool // job numbers shown, details beside the list
	list    rect
	details rect // w == 0: hidden
	footY   int  // the footer's row; at or past h when the terminal is too short to show it
}

func (m Model) size() (int, int) {
	if m.w == 0 || m.h == 0 {
		return defaultW, defaultH
	}
	return m.w, m.h
}

func (m Model) layout() layout {
	w, h := m.size()
	l := layout{w: w, h: h, wide: w >= m.o.Settings.UI.WideBreakpoint}
	body := rect{0, topRows, w, max(h-topRows-footerRows, 3)}
	l.list = body
	l.footY = body.y + body.h
	bottom := func() {
		dh := min(bottomRows, body.h-3)
		l.list.h = body.h - dh
		l.details = rect{0, body.y + l.list.h, w, dh}
	}
	switch pos := m.o.Settings.UI.DetailsPosition; {
	case w < minWidth || h < minHeight:
		l.compact, l.wide = true, false
	case pos == "hidden":
	case l.wide && pos == "bottom":
		bottom()
	case l.wide:
		dw := w * m.o.Settings.UI.DetailsWidthPercent / 100
		l.list.w = w - dw
		l.details = rect{w - dw, body.y, dw, body.h}
	case m.showDetails:
		bottom()
	}
	return l
}

// narrow says enter opens and closes the details pane under the list.
func (l layout) narrow() bool { return !l.compact && !l.wide }

// stride is the lines from one job's first line to the next one's;
// rowH the lines a job takes.
func (l layout) stride() int {
	if l.compact {
		return 1
	}
	return rowStride
}

func (l layout) rowH() int {
	if l.compact {
		return 1
	}
	return rowLines
}

// visibleRows is how many jobs fit in the list pane.
func (l layout) visibleRows() int {
	// no blank line after the last job
	return max((l.list.h-2+l.stride()-l.rowH())/l.stride(), 1)
}

// pane draws a rounded box with its title in the top border and body
// inside, cut or padded to fit. The focused pane has an accent border,
// the other a muted one.
func pane(t Theme, title, body string, w, h int, focused bool) string {
	b, line := t.G.border, t.muted
	inner := max(w-2, 0)
	title = truncate(title, max(inner-2, 0), t.G.ellipsis)
	head := t.muted.Render(title)
	if focused {
		line, head = t.accent, t.accent.Bold(true).Render(title)
	}
	fill := max(w-5-lipgloss.Width(title), 0)
	rows := make([]string, 0, max(h, 2))
	rows = append(rows, line.Render(b.TopLeft+b.Top)+" "+head+" "+line.Render(strings.Repeat(b.Top, fill)+b.TopRight))
	lines := strings.Split(body, "\n")
	for i := range max(h-2, 0) {
		s := ""
		if i < len(lines) {
			s = lines[i]
		}
		rows = append(rows, line.Render(b.Left)+fit(s, inner)+line.Render(b.Right))
	}
	rows = append(rows, line.Render(b.BottomLeft+strings.Repeat(b.Bottom, inner)+b.BottomRight))
	return strings.Join(rows, "\n")
}

// fit pads or cuts s to exactly w cells.
func fit(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return truncate(s, w, "")
}

// blank is w by h spaces: the content of a hit-test layer.
func blank(w, h int) string {
	return strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", max(w, 0))+"\n", max(h, 1)), "\n")
}
