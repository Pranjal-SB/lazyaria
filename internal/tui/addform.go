package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/tui/form"
)

// The add form's focus stops, in tab order.
const (
	stopLink = iota
	stopDir
	stopName
	stopPaused
	stopAdd
	stopCancel
	stops
)

// Hit-test ids of the form's parts, after layerModal.
var stopIDs = [stops]string{"link", "dir", "name", "paused", "add", "cancel"}

// Buttons under a duplicate.
const (
	dupAgain = iota
	dupShow
	dupCancel
)

var dupIDs = []string{"again", "show", "back"}

const formMaxW = 76

// addForm adds downloads: one or more links, a folder, an optional file
// name and whether to start paused. The daemon checks everything; the
// form shows what it says under the buttons.
type addForm struct {
	fields [3]form.Field // link, folder, file name
	paused form.Checkbox
	focus  int
	busy   bool // an add is on its way

	msg  string // what went wrong with the last try
	bad  bool
	open bool

	// A batch adds each link in turn. These collect its results until it
	// ends; a duplicate pauses it to ask what to do.
	batch   int // the batch running or last run from this form
	added   []*store.Job
	skipped []*store.Job
	failed  []addFailure
	unsure  []string     // links whose add timed out: they may be in the list
	chosen  bool         // "Show it" picked the job to select
	dup     *store.Job   // the job already in the list, while asking
	dupItem addItem      // the link that hit it
	rest    []addItem    // the links after it
	dupB    form.Buttons // the duplicate's buttons

	// Two or more links show as a checklist instead of the link field.
	list  bool
	rows  []string // the links, as they were given
	ticks []bool   // which rows are checked
	cur   int      // the row space toggles
	top   int      // the first row shown
}

func newAddForm(t Theme, dir string, links []string) addForm {
	f := addForm{open: true, paused: form.Checkbox{Label: labelStartPaused}}
	f.fields[stopLink] = form.NewField(labelLink, strings.Join(links, " "), placeLink, t.muted)
	f.fields[stopDir] = form.NewField(labelSaveTo, dir, "", t.muted)
	f.fields[stopName] = form.NewField(labelFileName, "", placeFileName, t.muted)
	f.setList(links)
	f.setFocus(stopLink)
	return f
}

// links are the links to add: the checked rows, or those in the field.
func (f addForm) links() []string {
	if !f.list {
		return strings.Fields(f.fields[stopLink].Value())
	}
	var out []string
	for i, l := range f.rows {
		if f.ticks[i] {
			out = append(out, l)
		}
	}
	return out
}

// items is one add per link. A file name only applies to a single link.
// The links are turned into adds, .torrent files read, when the batch runs.
func (f addForm) items() []addItem {
	var out []addItem
	links := f.links()
	for _, l := range links {
		p := daemon.AddParams{Dir: strings.TrimSpace(f.fields[stopDir].Value()), StartPaused: f.paused.Checked}
		if len(links) == 1 && !f.list {
			p.Out = strings.TrimSpace(f.fields[stopName].Value())
		}
		out = append(out, addItem{link: l, p: p})
	}
	return out
}

func (f *addForm) setFocus(i int) {
	f.focus = i
	for k := range f.fields {
		f.fields[k].Blur()
	}
	if i < len(f.fields) {
		f.fields[i].Focus()
	}
}

// move goes to the next focus stop, past a disabled file name.
func (f *addForm) move(delta int) {
	i := (f.focus + delta + stops) % stops
	if i == stopName && (f.list || len(f.links()) > 1) {
		i = (i + delta + stops) % stops
	}
	f.setFocus(i)
}

func (f *addForm) setWidth(w int) {
	for i := range f.fields {
		f.fields[i].SetWidth(max(w, 1))
	}
}

// formKey handles a key while the form is open.
func (m Model) formKey(k tea.KeyPressMsg) (Model, tea.Cmd) {
	f := &m.add
	if f.busy { // what is typed now would be lost when the batch ends
		if k.String() == "esc" {
			m.add = addForm{}
		}
		return m, nil
	}
	if f.dup != nil {
		switch k.String() {
		case "left", "shift+tab":
			f.dupB.Move(-1)
		case "right", "tab":
			f.dupB.Move(1)
		case "enter", "space":
			return m.dupPress(f.dupB.Focus)
		case "esc":
			return m.dupPress(dupCancel)
		}
		return m, nil
	}
	// the checklist's cursor goes first; focus moves at its ends
	if f.focus == stopLink && f.list && (k.String() == "down" && m.listMove(1) || k.String() == "up" && m.listMove(-1)) {
		return m, nil
	}
	switch k.String() {
	case "esc":
		m.add = addForm{}
		return m, nil
	case "tab", "down":
		f.move(1)
		return m, nil
	case "shift+tab", "up":
		f.move(-1)
		return m, nil
	case "enter":
		if f.focus == stopCancel {
			m.add = addForm{}
			return m, nil
		}
		return m.submitForm()
	case "space":
		switch f.focus {
		case stopLink:
			if f.list {
				f.ticks[f.cur] = !f.ticks[f.cur]
				return m, nil
			}
		case stopPaused:
			f.paused.Toggle()
			return m, nil
		case stopAdd:
			return m.submitForm()
		case stopCancel:
			m.add = addForm{}
			return m, nil
		}
	}
	return m.formInput(k)
}

// formInput sends a key or a paste to the focused text field. A paste
// with no text field focused goes to the link.
func (m Model) formInput(msg tea.Msg) (Model, tea.Cmd) {
	f := &m.add
	if f.busy || f.dup != nil {
		return m, nil
	}
	if _, ok := msg.(tea.PasteMsg); ok && f.focus >= len(f.fields) {
		f.setFocus(stopLink)
	}
	if f.focus >= len(f.fields) {
		return m, nil
	}
	if f.list && f.focus == stopLink {
		return m, nil // the checklist is picked, not typed into
	}
	var cmd tea.Cmd
	f.fields[f.focus], cmd = f.fields[f.focus].Update(msg)
	if _, ok := msg.(tea.PasteMsg); ok && f.focus == stopLink && len(f.links()) >= 2 {
		f.setList(f.links())
	}
	return m, cmd
}

// formClick focuses or presses the part of the form under the pointer.
func (m Model) formClick(id string) (Model, tea.Cmd) {
	f := &m.add
	if f.dup != nil {
		if i := slices.Index(dupIDs, id); i >= 0 {
			return m.dupPress(i)
		}
		return m, nil
	}
	if f.busy {
		if id == stopIDs[stopCancel] {
			m.add = addForm{}
		}
		return m, nil
	}
	if m, ok := m.listClick(id); ok {
		return m, nil
	}
	for i, s := range stopIDs {
		if id != s {
			continue
		}
		switch i {
		case stopAdd:
			return m.submitForm()
		case stopCancel:
			m.add = addForm{}
		case stopPaused:
			f.setFocus(i)
			f.paused.Toggle()
		default:
			f.setFocus(i)
		}
	}
	return m, nil
}

// home shortens a path under the home folder to ~/...
func home(p string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" && (p == h || strings.HasPrefix(p, h+string(os.PathSeparator))) {
		return "~" + p[len(h):]
	}
	return p
}

// formView draws the form at most maxW wide, with its clickable parts.
func (m Model) formView(maxW int) overlay {
	t, f := m.theme, m.add
	w := min(formMaxW, maxW)
	in := w - 6 // border and two cells of padding each side
	var lines []string
	var hits []hitArea
	add := func(s string) { lines = append(lines, "  "+s) }
	hitLine := func(id string, w int) {
		hits = append(hits, hitArea{layerModal + id, 3, len(lines), w, 1})
	}
	lines = append(lines, "")
	for i, fl := range f.fields {
		if f.list && i == stopName {
			continue
		}
		if f.list && i == stopLink {
			m.listView(in, add, hitLine)
			lines = append(lines, "")
			continue
		}
		label := t.muted.Render(fl.Label)
		if i == f.focus {
			label = t.accent.Bold(true).Render(fl.Label)
		}
		add(label)
		hitLine(stopIDs[i], in)
		v := fl.View()
		if i == stopName && len(f.links()) > 1 {
			v = t.muted.Render(textNameOneLink)
		}
		add(v)
		lines = append(lines, "")
	}
	box := f.paused.View()
	if f.focus == stopPaused {
		box = t.accent.Bold(true).Render(box)
	}
	hitLine(stopIDs[stopPaused], lipgloss.Width(box))
	add(box)
	lines = append(lines, "")
	btn := func(ids []string, b form.Buttons, danger int) {
		row, spans := b.View(func(i int, focused bool) lipgloss.Style {
			st := lipgloss.NewStyle()
			if focused {
				st = t.accent.Bold(true).Reverse(true)
			} else if i == danger {
				st = t.bad
			} else if f.list && ids[i] == stopIDs[stopAdd] && f.checked() == 0 {
				st = t.muted // nothing to add
			}
			if m.hover == layerModal+ids[i] {
				st = st.Bold(true)
			}
			return st
		})
		for i, s := range spans {
			hits = append(hits, hitArea{layerModal + ids[i], 3 + s[0], len(lines), s[1], 1})
		}
		add(row)
	}
	focusBtn := -1
	if f.focus >= stopAdd {
		focusBtn = f.focus - stopAdd
	}
	if f.dup == nil {
		addLabel := btnAdd
		if f.list {
			addLabel = fmt.Sprintf(fmtAddN, f.checked())
		}
		btn(stopIDs[stopAdd:], form.Buttons{Labels: []string{addLabel, btnCancel}, Focus: focusBtn}, -1)
		lines = append(lines, "")
	}
	if f.msg != "" {
		msg := truncate(f.msg, in, t.G.ellipsis)
		if f.bad {
			msg = t.bad.Render(msg)
		}
		add(msg)
		lines = append(lines, "")
		if f.dup != nil {
			btn(dupIDs, f.dupB, -1)
			lines = append(lines, "")
		}
	}
	hits = clipHits(hits, w)
	return overlay{pane(t, titleAdd, strings.Join(lines, "\n"), w, len(lines)+2, true), hits}
}

// clipHits drops parts cut off by a narrow box, which cannot be clicked.
func clipHits(hs []hitArea, w int) []hitArea {
	var out []hitArea
	for _, h := range hs {
		h.w = min(h.w, w-1-h.x)
		if h.w > 0 {
			out = append(out, h)
		}
	}
	return out
}
