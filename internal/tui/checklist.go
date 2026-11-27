package tui

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

// itemPrefix starts the hit-test id of a checklist row.
const itemPrefix = "item"

// maxListRows is how many rows the checklist shows before it scrolls.
const maxListRows = 8

// setList puts the links in the field and shows them as a checklist, all
// checked, when there are two or more. One link, or none, stays in the
// one-line field.
func (f *addForm) setList(links []string) {
	f.fields[stopLink].SetValue(strings.Join(links, " "))
	f.list, f.cur, f.top = len(links) >= 2, 0, 0
	f.rows, f.ticks = nil, nil
	if f.list {
		f.rows = append([]string(nil), links...)
		f.ticks = make([]bool, len(links))
		for i := range f.ticks {
			f.ticks[i] = true
		}
	}
}

// checked is how many rows are ticked.
func (f addForm) checked() int {
	n := 0
	for _, t := range f.ticks {
		if t {
			n++
		}
	}
	return n
}

// checkRows is how many rows fit: the form's other lines take 17 of the screen.
func (m Model) checkRows() int { return min(max(m.h-17, 1), maxListRows) }

// listMove puts the cursor on another row and scrolls it into view. It
// reports false at either end, where the key moves focus instead.
func (m *Model) listMove(delta int) bool {
	f := &m.add
	i := f.cur + delta
	if i < 0 || i >= len(f.rows) {
		return false
	}
	f.cur = i
	n := m.checkRows()
	f.top = min(max(f.top, i-n+1), i)
	return true
}

// listScroll scrolls the rows by the wheel, keeping the cursor on screen.
func (m *Model) listScroll(delta int) {
	f := &m.add
	n := m.checkRows()
	f.top = min(max(f.top+delta, 0), max(len(f.rows)-n, 0))
	f.cur = min(max(f.cur, f.top), f.top+n-1)
}

// maskLink hides the user info of a link, for showing it.
func maskLink(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return textutil.StripUserinfo(s, "***@")
	}
	if u.User == nil {
		return s
	}
	u.User = nil // url.User("***") would print as %2A%2A%2A
	return strings.Replace(u.String(), "://", "://***@", 1)
}

// listView draws the checklist: a label, the visible rows and, when some
// are hidden, how many.
func (m Model) listView(in int, add func(string), hitLine func(string, int)) {
	t, f := m.theme, m.add
	label := labelLinks + "  " + fmt.Sprintf(fmtChecked, f.checked(), len(f.rows))
	if f.focus == stopLink {
		add(t.accent.Bold(true).Render(label))
	} else {
		add(t.muted.Render(label))
	}
	n := m.checkRows()
	top := min(f.top, max(len(f.rows)-n, 0))
	for i := top; i < min(top+n, len(f.rows)); i++ {
		row := truncate(listBox(f.ticks[i])+maskLink(f.rows[i]), in, t.G.ellipsis)
		if i == f.cur && f.focus == stopLink {
			row = t.accent.Bold(true).Render(row)
		}
		hitLine(itemPrefix+strconv.Itoa(i), in)
		add(row)
	}
	above, below := top, len(f.rows)-top-n
	switch {
	case above > 0 && below > 0:
		add(t.muted.Render(fmt.Sprintf(fmtRowsAbove, above) + ", " + fmt.Sprintf(fmtRowsBelow, below)))
	case above > 0:
		add(t.muted.Render(fmt.Sprintf(fmtRowsAbove, above)))
	case below > 0:
		add(t.muted.Render(fmt.Sprintf(fmtRowsBelow, below)))
	}
}

func listBox(on bool) string {
	if on {
		return "[x] "
	}
	return "[ ] "
}

// listClick toggles the row a click landed on.
func (m Model) listClick(id string) (Model, bool) {
	i, err := strconv.Atoi(strings.TrimPrefix(id, itemPrefix))
	f := &m.add
	if !strings.HasPrefix(id, itemPrefix) || err != nil || !f.list || i < 0 || i >= len(f.rows) {
		return m, false
	}
	f.setFocus(stopLink)
	f.cur = i
	f.ticks[i] = !f.ticks[i]
	return m, true
}
