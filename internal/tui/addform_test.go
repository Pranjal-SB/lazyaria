package tui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

const pasted = "look: https://example.com/files/ubuntu.iso and https://example.com/files/paper.pdf."

func update(m Model, msgs ...tea.Msg) Model {
	for _, msg := range msgs {
		r, _ := m.Update(msg)
		m = r.(Model)
	}
	return m
}

func TestPasteOpensFormPrefilled(t *testing.T) {
	m := update(sampleModel(t, 120, 32), tea.PasteMsg{Content: pasted})
	if !m.add.open {
		t.Fatal("a paste with links did not open the form")
	}
	if got := m.add.links(); !slices.Equal(got, []string{"https://example.com/files/ubuntu.iso", "https://example.com/files/paper.pdf"}) {
		t.Fatalf("links %q", got)
	}
}

func TestPasteIntoFocusedFieldNotGlobal(t *testing.T) {
	m := update(sampleModel(t, 120, 32), key("a"), tea.PasteMsg{Content: pasted})
	if got := m.add.fields[stopLink].Value(); got != pasted {
		t.Fatalf("link field %q, want the paste as it is", got)
	}
}

func TestPasteWithoutLinksToasts(t *testing.T) {
	m := update(sampleModel(t, 120, 32), tea.PasteMsg{Content: "just words"})
	if m.add.open || m.toast.text != textNoLinks {
		t.Fatalf("form open %v, toast %q", m.add.open, m.toast.text)
	}
}

func TestPasteDetectionOff(t *testing.T) {
	m := sampleModel(t, 120, 32)
	m.o.Settings.UI.PasteDetection = false
	m = update(m, tea.PasteMsg{Content: pasted})
	if m.add.open || m.toast.text != "" {
		t.Fatalf("paste detection off: form open %v, toast %q", m.add.open, m.toast.text)
	}
}

func TestAddParams(t *testing.T) {
	f := newAddForm(NewTheme("unicode"), "/dl", []string{"https://a.example/one.iso"})
	f.fields[stopDir].SetValue("/data")
	f.fields[stopName].SetValue("mine.iso")
	f.paused.Toggle()
	want := []daemon.AddParams{{URIs: []string{"https://a.example/one.iso"}, Dir: "/data", Out: "mine.iso", StartPaused: true}}
	if got := f.params(); !slices.EqualFunc(got, want, eqParams) {
		t.Fatalf("params %+v, want %+v", got, want)
	}
	f.fields[stopLink].SetValue("https://a.example/one.iso  https://b.example/two.iso")
	got := f.params()
	if len(got) != 2 || got[0].Out != "" || got[1].URIs[0] != "https://b.example/two.iso" || got[1].Dir != "/data" || !got[1].StartPaused {
		t.Fatalf("two links give %+v; want two adds without the file name", got)
	}
	f.setFocus(stopDir)
	f.move(1)
	if f.focus != stopPaused {
		t.Fatalf("tab from the folder went to stop %d; the file name is off for two links", f.focus)
	}
}

func eqParams(a, b daemon.AddParams) bool {
	return slices.Equal(a.URIs, b.URIs) && a.Dir == b.Dir && a.Out == b.Out && a.StartPaused == b.StartPaused && a.Force == b.Force
}

func dupDaemon() *fakeDaemon {
	d := sampleDaemon()
	d.add = func(p daemon.AddParams) (*store.Job, error) {
		if p.Force {
			return &store.Job{ID: "new", Num: 18, Name: "paper.pdf", State: store.Queued}, nil
		}
		return nil, &daemon.DuplicateError{Existing: &store.Job{ID: "paper.pdf", Num: 9, Name: "paper.pdf", State: store.Done}}
	}
	return d
}

func TestAddSuccessSelectsAndToasts(t *testing.T) {
	d := sampleDaemon()
	d.add = func(daemon.AddParams) (*store.Job, error) {
		return &store.Job{ID: "fedora-43-workstation.iso", Num: 11, Name: "fedora-43-workstation.iso", State: store.Queued}, nil
	}
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "aria2")
	u.Send(tea.PasteMsg{Content: "https://example.com/files/fedora-43-workstation.iso"})
	u.waitFor(t, titleAdd)
	u.Send(key("enter"))
	u.waitFor(t, "Added #11 fedora-43-workstation.iso")
	m := u.quit(t)
	if m.add.open || m.tab != 1 || m.selected().Num != 11 {
		t.Fatalf("after adding: form open %v, tab %d, selected %v", m.add.open, m.tab, m.selected())
	}
}

func TestDuplicateDownloadAgainForces(t *testing.T) {
	d := dupDaemon()
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "aria2")
	u.Send(tea.PasteMsg{Content: "https://example.com/files/paper.pdf"})
	u.waitFor(t, titleAdd)
	u.Send(key("enter"))
	u.waitFor(t, "Already in the list as #9 (done).")
	u.Send(key("enter"))
	waitCalls(t, d,
		"add https://example.com/files/paper.pdf dir=/dl out= paused=false force=false",
		"add https://example.com/files/paper.pdf dir=/dl out= paused=false force=true")
	u.waitFor(t, "Added #18 paper.pdf")
	u.quit(t)
}

func TestDuplicateShowItSelects(t *testing.T) {
	d := dupDaemon()
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "aria2")
	u.Send(tea.PasteMsg{Content: "https://example.com/files/paper.pdf"})
	u.waitFor(t, titleAdd)
	u.Send(key("enter"))
	u.waitFor(t, "Already in the list")
	u.Send(tea.KeyPressMsg{Code: tea.KeyRight})
	u.Send(key("enter"))
	m := u.quit(t)
	if m.add.open || m.tab != 2 || m.selected() == nil || m.selected().Num != 9 {
		t.Fatalf("after show it: form open %v, tab %d, selected %v", m.add.open, m.tab, m.selected())
	}
}

func TestNoSpaceMessage(t *testing.T) {
	m := update(sampleModel(t, 120, 32), key("a"))
	m, _ = m.added(addMsg{err: &daemon.NoSpaceError{Dir: "/data", Need: 6 << 30, Free: 2254857830}})
	if want := "Not enough space in /data: needs 6.0 GiB, 2.1 GiB free."; m.add.msg != want || !m.add.bad {
		t.Fatalf("message %q (bad %v), want %q", m.add.msg, m.add.bad, want)
	}
}

func TestFormGolden(t *testing.T) {
	for _, tc := range []goldenCase{
		{name: "add_form_120x32_ansi", w: 120, h: 32, d: sampleDaemon(), keys: []tea.Msg{key("a")}},
		{name: "add_form_80x24_ansi", w: 80, h: 24, d: sampleDaemon(), keys: []tea.Msg{tea.PasteMsg{Content: pasted}}},
		{name: "add_duplicate_120x32_ansi", w: 120, h: 32, d: dupDaemon(),
			keys: []tea.Msg{tea.PasteMsg{Content: "https://example.com/files/paper.pdf"}, key("enter")}, wait: "Already in the list"},
	} {
		tc.profile = colorprofile.ANSI
		t.Run(tc.name, func(t *testing.T) { runGolden(t, tc) })
	}
}
