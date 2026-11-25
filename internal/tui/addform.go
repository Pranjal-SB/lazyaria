package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
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

	msg  string // the daemon's answer to the last try
	bad  bool
	dup  *store.Job         // the job already in the list, if that was the answer
	rest []daemon.AddParams // what is still to add after a duplicate
	dupB form.Buttons       // the duplicate's buttons
	open bool
}

type addMsg struct {
	added []*store.Job
	err   error
	rest  []daemon.AddParams // the one that failed and the ones after it
}

func newAddForm(t Theme, dir string, links []string) addForm {
	f := addForm{open: true, paused: form.Checkbox{Label: labelStartPaused}}
	f.fields[stopLink] = form.NewField(labelLink, strings.Join(links, " "), placeLink, t.muted)
	f.fields[stopDir] = form.NewField(labelSaveTo, dir, "", t.muted)
	f.fields[stopName] = form.NewField(labelFileName, "", placeFileName, t.muted)
	f.setFocus(stopLink)
	return f
}

func (f addForm) links() []string { return strings.Fields(f.fields[stopLink].Value()) }

// params is one add per link. A file name only applies to a single link.
func (f addForm) params() []daemon.AddParams {
	var ps []daemon.AddParams
	links := f.links()
	for _, l := range links {
		p := daemon.AddParams{URIs: []string{l}, Dir: strings.TrimSpace(f.fields[stopDir].Value()), StartPaused: f.paused.Checked}
		if len(links) == 1 {
			p.Out = strings.TrimSpace(f.fields[stopName].Value())
		}
		ps = append(ps, p)
	}
	return ps
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
	if i == stopName && len(f.links()) > 1 {
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
	if f.dup != nil {
		switch k.String() {
		case "left", "shift+tab":
			f.dupB.Move(-1)
		case "right", "tab":
			f.dupB.Move(1)
		case "enter", "space":
			return m.dupPress(f.dupB.Focus)
		case "esc":
			m.add = addForm{}
		}
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
		return m.submit(f.params())
	case "space":
		switch f.focus {
		case stopPaused:
			f.paused.Toggle()
			return m, nil
		case stopAdd:
			return m.submit(f.params())
		case stopCancel:
			m.add = addForm{}
			return m, nil
		}
	}
	return m.formInput(k)
}

// formInput sends a key or a paste to the focused text field.
func (m Model) formInput(msg tea.Msg) (Model, tea.Cmd) {
	f := &m.add
	if f.focus >= len(f.fields) {
		return m, nil
	}
	var cmd tea.Cmd
	f.fields[f.focus], cmd = f.fields[f.focus].Update(msg)
	return m, cmd
}

// formClick focuses or presses the part of the form under the pointer.
func (m Model) formClick(id string) (Model, tea.Cmd) {
	f := &m.add
	for i, d := range dupIDs {
		if id == d && f.dup != nil {
			return m.dupPress(i)
		}
	}
	for i, s := range stopIDs {
		if id != s {
			continue
		}
		switch i {
		case stopAdd:
			return m.submit(f.params())
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

func (m Model) dupPress(i int) (Model, tea.Cmd) {
	f := &m.add
	switch i {
	case dupAgain:
		rest := append([]daemon.AddParams(nil), f.rest...)
		rest[0].Force = true
		return m.submit(rest)
	case dupShow:
		if f.dup != nil && slices.ContainsFunc(m.jobs, func(j *store.Job) bool { return j.ID == f.dup.ID }) {
			m.selWant = f.dup.ID
		}
	}
	m.add = addForm{}
	return m, nil
}

// submit adds each link in turn and stops at the first the daemon refuses.
func (m Model) submit(ps []daemon.AddParams) (Model, tea.Cmd) {
	if len(ps) == 0 || m.d == nil || m.add.busy {
		return m, nil
	}
	m.add.busy = true
	d := m.d
	return m, m.cmd(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		var added []*store.Job
		for i, p := range ps {
			j, err := d.Add(ctx, p)
			if err != nil {
				return addMsg{added, err, ps[i:]}
			}
			added = append(added, j)
		}
		return addMsg{added: added}
	})
}

// added closes the form on success and selects the new job; otherwise it
// shows what the daemon said under the form.
func (m Model) added(msg addMsg) (Model, tea.Cmd) {
	if !m.add.open {
		return m, nil
	}
	f := &m.add
	f.busy, f.msg, f.bad, f.dup = false, "", false, nil
	if msg.err == nil {
		m.add = addForm{}
		if len(msg.added) == 0 || msg.added[len(msg.added)-1] == nil {
			return m, nil
		}
		j := msg.added[len(msg.added)-1]
		m.selWant = j.ID
		text := fmt.Sprintf(fmtAdded, j.Num, j.Name)
		if len(msg.added) > 1 {
			text = fmt.Sprintf(fmtAddedMany, len(msg.added))
		}
		return m, m.showToast(toast{text: text})
	}
	dup, dupOK := errors.AsType[*daemon.DuplicateError](msg.err)
	skip, skipOK := errors.AsType[*daemon.SkippedError](msg.err)
	ns, nsOK := errors.AsType[*daemon.NoSpaceError](msg.err)
	switch {
	case dupOK && dup.Existing != nil:
		f.dup, f.rest = dup.Existing, msg.rest
	case skipOK && skip.Existing != nil:
		f.dup, f.rest = skip.Existing, msg.rest
	case dupOK:
		f.msg, f.bad = fmt.Sprintf(fmtFileThere, home(dup.Path)), true
		return m, nil
	case nsOK:
		f.msg, f.bad = fmt.Sprintf(fmtNoSpace, home(ns.Dir), textutil.Bytes(int64(ns.Need), m.o.Settings.UI.Units == "si"),
			textutil.Bytes(int64(ns.Free), m.o.Settings.UI.Units == "si")), true
		return m, nil
	default:
		f.msg, f.bad = msg.err.Error(), true
		return m, nil
	}
	label := f.dup.Label
	if label == "" {
		label = string(f.dup.State)
	}
	f.msg = fmt.Sprintf(fmtDuplicate, f.dup.Num, label)
	f.dupB = form.Buttons{Labels: []string{btnAgain, btnShow, btnCancel}}
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
		btn(stopIDs[stopAdd:], form.Buttons{Labels: []string{btnAdd, btnCancel}, Focus: focusBtn}, -1)
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
