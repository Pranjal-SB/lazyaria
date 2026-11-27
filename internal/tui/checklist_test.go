package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

const three = "https://e.example/ok-1 https://e.example/ok-2 https://e.example/ok-3"

func TestPasteManyOpensChecklist(t *testing.T) {
	m := update(sampleModel(t, 120, 32), tea.PasteMsg{Content: pasted})
	if !m.add.list || len(m.add.rows) != 2 || m.add.checked() != 2 {
		t.Fatalf("list %v, rows %q, checked %d", m.add.list, m.add.rows, m.add.checked())
	}
	v := m.View().Content
	if !strings.Contains(v, "[x] https://example.com/files/ubuntu.iso") || strings.Contains(v, "File name") {
		t.Fatalf("checklist not drawn, or the file name shows:\n%s", v)
	}
}

func TestSingleLinkKeepsField(t *testing.T) {
	m := update(sampleModel(t, 120, 32), tea.PasteMsg{Content: "https://e.example/ok-1"})
	if m.add.list || !strings.Contains(m.View().Content, "File name") {
		t.Fatal("one link must keep the one-line field and the file name")
	}
}

func TestChecklistUntickedNotAdded(t *testing.T) {
	m, d := batchModel(t)
	m = update(m, tea.PasteMsg{Content: three}, key("down"), key("space"))
	m, c := step(m, key("enter"))
	m = update(m, runAdds(c)...)
	if want := []string{addCall("ok-1"), addCall("ok-3")}; !slices.Equal(d.called(), want) || m.add.open {
		t.Fatalf("calls %q, form open %v; want %q", d.called(), m.add.open, want)
	}
}

func TestChecklistAddCountFollowsTicks(t *testing.T) {
	m, d := batchModel(t)
	m = update(m, tea.PasteMsg{Content: three})
	if !strings.Contains(m.View().Content, "Add 3") {
		t.Fatal("no Add 3")
	}
	m = update(m, key("space"), key("down"), key("space"), key("down"), key("space"))
	if v := m.View().Content; !strings.Contains(v, "Add 0") || m.add.checked() != 0 {
		t.Fatalf("no Add 0:\n%s", v)
	}
	m = update(m, key("tab"), key("tab")) // past the folder and the paused box, to Add
	m, c := step(m, key("enter"))
	if c != nil || m.add.busy || len(d.called()) != 0 {
		t.Fatalf("Add with nothing checked did something (focus %d)", m.add.focus)
	}
}

func TestChecklistScroll(t *testing.T) {
	var links []string
	for i := 1; i <= 50; i++ {
		links = append(links, fmt.Sprintf("https://e.example/ok-%d", i))
	}
	last := "ok-50"
	m := update(sampleModel(t, 80, 24), tea.PasteMsg{Content: strings.Join(links, " ")})
	if strings.Contains(m.View().Content, last) {
		t.Fatal("the last row shows at the start")
	}
	byKey := m
	for range 49 {
		byKey = update(byKey, key("down"))
	}
	if byKey.add.focus != stopLink || !strings.Contains(byKey.View().Content, last) {
		t.Fatalf("last row not reached by key (focus %d)", byKey.add.focus)
	}
	byWheel := m
	for range 60 {
		byWheel = update(byWheel, tea.MouseWheelMsg{X: 40, Y: 10, Button: tea.MouseWheelDown})
	}
	if !strings.Contains(byWheel.View().Content, last) {
		t.Fatal("last row not reached by the wheel")
	}
}

func TestChecklistMasksUserinfo(t *testing.T) {
	m := update(sampleModel(t, 120, 32), tea.PasteMsg{Content: "https://bob:hunter2@e.example/a.iso https://e.example/b.iso"})
	v := m.View().Content
	if strings.Contains(v, "hunter2") || strings.Contains(v, "bob") || !strings.Contains(v, "https://***@e.example/a.iso") {
		t.Fatalf("user info shows or is not masked:\n%s", v)
	}
}

func TestChecklistClickToggles(t *testing.T) {
	m := update(sampleModel(t, 120, 32), tea.PasteMsg{Content: three})
	var row hitArea
	ov := m.formView(m.layout().w - 2)
	for _, h := range ov.hits {
		if h.id == layerModal+itemPrefix+"1" {
			row = h
		}
	}
	x := (m.layout().w-lipgloss.Width(ov.body))/2 + row.x
	y := (m.layout().h-lipgloss.Height(ov.body))/2 + row.y
	m = click(m, x, y)
	if m.add.ticks[1] || !m.add.ticks[0] || m.add.checked() != 2 {
		t.Fatalf("ticks %v after a click on row 2", m.add.ticks)
	}
}

func TestChecklistGolden(t *testing.T) {
	var many []string
	for i := 1; i <= 12; i++ {
		many = append(many, fmt.Sprintf("https://example.com/files/part-%02d.bin", i))
	}
	paste := tea.PasteMsg{Content: strings.Join(many, " ")}
	for _, tc := range []goldenCase{
		{name: "add_checklist_120x32_ansi", w: 120, h: 32, d: sampleDaemon(), keys: []tea.Msg{paste, key("space")}},
		{name: "add_checklist_80x24_ansi", w: 80, h: 24, d: sampleDaemon(), keys: []tea.Msg{paste}},
	} {
		tc.profile = colorprofile.ANSI
		t.Run(tc.name, func(t *testing.T) { runGolden(t, tc) })
	}
}
