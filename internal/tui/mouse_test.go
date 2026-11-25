package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// sampleModel is the sample list at w by h, straight through Update.
func sampleModel(t *testing.T, w, h int) Model {
	t.Helper()
	var m tea.Model = New(options(t))
	m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	d := sampleDaemon()
	m, _ = m.Update(connectedMsg{d: d, snap: d.snap})
	return m.(Model)
}

func click(m Model, x, y int) Model {
	r, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return r.(Model)
}

func TestClickSelectsRow(t *testing.T) {
	m := click(sampleModel(t, 120, 32), 10, 8) // the second job's first line
	if j := m.selected(); j == nil || j.Name != "conference-talk-1080p.mkv" {
		t.Fatalf("selected %v after a click on the second row", j)
	}
	m = click(m, 20, 2) // the Waiting tab
	if m.tab != 1 {
		t.Fatalf("tab %d after a click on Waiting", m.tab)
	}
}

func TestClickFooterHintRunsAction(t *testing.T) {
	m := sampleModel(t, 120, 32)
	_, areas := m.footer(m.layout())
	for _, a := range areas {
		if a.id == layerBtn+actRemove {
			m = click(m, a.x+1, m.layout().h-1)
		}
	}
	if !m.remove.open() || m.remove.name != "ubuntu-26.04-desktop-amd64.iso" {
		t.Fatalf("a click on the remove key gave dialog %+v", m.remove)
	}
}

func TestClickOutsideModalCloses(t *testing.T) {
	m := sampleModel(t, 120, 32)
	r, _ := m.Update(key("x"))
	m = click(r.(Model), 60, 13) // inside the dialog, above the buttons
	if !m.remove.open() {
		t.Fatal("a click inside the dialog closed it")
	}
	m = click(m, 1, 1)
	if m.remove.open() {
		t.Fatal("a click outside the dialog left it open")
	}
}

func TestClickModalButton(t *testing.T) {
	m := sampleModel(t, 120, 32)
	r, _ := m.Update(key("x"))
	m = r.(Model)
	ov := m.removeView(m.layout().w - 2)
	var cancel hitArea
	for _, h := range ov.hits {
		if h.id == layerModal+choiceCancel {
			cancel = h
		}
	}
	x := (m.layout().w-lipgloss.Width(ov.body))/2 + cancel.x
	y := (m.layout().h-lipgloss.Height(ov.body))/2 + cancel.y
	if kind, id := hit(m.compose(), x, y); kind != "modal" || id != choiceCancel {
		t.Fatalf("hit at the cancel button is %s:%s", kind, id)
	}
	m = click(m, x, y)
	if m.remove.open() {
		t.Fatal("cancel left the dialog open")
	}
}

func TestHoverBoldsRow(t *testing.T) {
	m := sampleModel(t, 120, 32)
	r, _ := m.Update(tea.MouseMotionMsg{X: 10, Y: 8})
	if got := r.(Model).hover; got != layerRow+"conference-talk-1080p.mkv" {
		t.Fatalf("hover %q", got)
	}
}

func TestWheelMovesSelection(t *testing.T) {
	m := sampleModel(t, 120, 32)
	r, _ := m.Update(tea.MouseWheelMsg{X: 10, Y: 10, Button: tea.MouseWheelDown})
	if j := r.(Model).selected(); j == nil || j.Name != "conference-talk-1080p.mkv" {
		t.Fatalf("selected %v after the wheel", j)
	}
}

func TestMouseOffNoCapture(t *testing.T) {
	o := options(t)
	o.Settings.UI.Mouse = "off"
	if mm := New(o).View().MouseMode; mm != tea.MouseModeNone {
		t.Fatalf("mouse mode %v with ui.mouse = off", mm)
	}
	o.Settings.UI.Mouse = "all"
	if mm := New(o).View().MouseMode; mm != tea.MouseModeAllMotion {
		t.Fatalf("mouse mode %v with ui.mouse = all", mm)
	}
}
