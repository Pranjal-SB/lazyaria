package tui

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
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
	want := daemon.AddParams{URIs: []string{"https://a.example/one.iso"}, Dir: "/data", Out: "mine.iso", StartPaused: true}
	got := f.items()
	if len(got) != 1 {
		t.Fatalf("items %+v", got)
	}
	if p, err := resolve(got[0]); err != nil || !eqParams(p, want) {
		t.Fatalf("add %+v (%v), want %+v", p, err, want)
	}
	f.fields[stopLink].SetValue("https://a.example/one.iso  https://b.example/two.iso nonsense")
	got = f.items()
	if len(got) != 3 || got[0].p.Out != "" || got[1].link != "https://b.example/two.iso" || got[1].p.Dir != "/data" || !got[1].p.StartPaused {
		t.Fatalf("three links give %+v; want three adds without the file name", got)
	}
	if _, err := resolve(got[2]); err == nil || got[2].link != "nonsense" {
		t.Fatalf("a word that is no link gave %+v, %v", got[2], err)
	}
	f.setFocus(stopDir)
	f.move(1)
	if f.focus != stopPaused {
		t.Fatalf("tab from the folder went to stop %d; the file name is off for several links", f.focus)
	}
}

func eqParams(a, b daemon.AddParams) bool {
	return slices.Equal(a.URIs, b.URIs) && a.Dir == b.Dir && a.Out == b.Out && a.StartPaused == b.StartPaused && a.Force == b.Force
}

func dupDaemon() *fakeDaemon {
	d := sampleDaemon()
	d.add = func(_ context.Context, p daemon.AddParams) (*store.Job, error) {
		if p.Force {
			return &store.Job{ID: "new", Num: 18, Name: "paper.pdf", State: store.Queued}, nil
		}
		return nil, &daemon.DuplicateError{Existing: &store.Job{ID: "paper.pdf", Num: 9, Name: "paper.pdf", State: store.Done}}
	}
	return d
}

func TestAddSuccessSelectsAndToasts(t *testing.T) {
	d := sampleDaemon()
	d.add = func(context.Context, daemon.AddParams) (*store.Job, error) {
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
	got := addErrText(&daemon.NoSpaceError{Dir: "/data", Need: 6 << 30, Free: 2254857830}, false)
	if want := "Not enough space in /data: needs 6.0 GiB, 2.1 GiB free."; got != want {
		t.Fatalf("message %q, want %q", got, want)
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

// batchDaemon adds ok-*, refuses bad-* and no-space-*, and knows dup-* already.
func batchDaemon() *fakeDaemon {
	d := sampleDaemon()
	d.add = func(ctx context.Context, p daemon.AddParams) (*store.Job, error) {
		name := path.Base(p.URIs[0])
		switch {
		case strings.HasPrefix(name, "slow"):
			<-ctx.Done() // the daemon is stuck; it may add it later
			return nil, ctx.Err()
		case strings.HasPrefix(name, "bad"):
			return nil, &daemon.BadRequestError{Err: errors.New("the server says no")}
		case strings.HasPrefix(name, "no-space"):
			return nil, &daemon.NoSpaceError{Dir: "/dl", Need: 2 << 30, Free: 1 << 30}
		case strings.HasPrefix(name, "dup") && !p.Force:
			return nil, &daemon.DuplicateError{Existing: &store.Job{ID: "paper.pdf", Num: 9, State: store.Done}}
		}
		return &store.Job{ID: name, Num: 20, Name: name, State: store.Queued}, nil
	}
	return d
}

const batchLinks = "https://e.example/ok-1 https://e.example/bad-1 https://e.example/dup-1 https://e.example/no-space-1 https://e.example/ok-2"

func addCall(name string) string {
	return "add https://e.example/" + name + " dir=/dl out= paused=false force=false"
}

// A batch goes past refused links, pauses at a duplicate, goes on after
// the answer, and leaves only the links not added in the field.
func TestBatchKeepsOnlyFailedLinks(t *testing.T) {
	// esc skips the duplicate; so does Cancel, left of Download again
	for _, answer := range [][]tea.Msg{{key("esc")}, {tea.KeyPressMsg{Code: tea.KeyLeft}, key("enter")}} {
		d := batchDaemon()
		u := startTUI(t, options(t, d), 120, 32)
		u.waitFor(t, "aria2")
		u.Send(tea.PasteMsg{Content: batchLinks})
		u.waitFor(t, titleAdd)
		u.Send(key("enter"))
		u.waitFor(t, "Already in the list as #9")
		waitCalls(t, d, addCall("ok-1"), addCall("bad-1"), addCall("dup-1"))
		for _, k := range answer {
			u.Send(k)
		}
		waitCalls(t, d, addCall("ok-1"), addCall("bad-1"), addCall("dup-1"), addCall("no-space-1"), addCall("ok-2"))
		u.waitFor(t, "2 links were not added. The first: the server says no")
		m := u.quit(t)
		if got := m.add.fields[stopLink].Value(); got != "https://e.example/bad-1 https://e.example/no-space-1" {
			t.Fatalf("link field %q, want only the two links not added", got)
		}
	}
}

func TestBatchDownloadAgainGoesOn(t *testing.T) {
	d := batchDaemon()
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "aria2")
	u.Send(tea.PasteMsg{Content: "https://e.example/dup-1 https://e.example/ok-1"})
	u.waitFor(t, titleAdd)
	u.Send(key("enter"))
	u.waitFor(t, "Already in the list as #9")
	u.Send(key("enter")) // Download again
	waitCalls(t, d, addCall("dup-1"), "add https://e.example/dup-1 dir=/dl out= paused=false force=true", addCall("ok-1"))
	u.waitFor(t, "Added 2 downloads")
	if m := u.quit(t); m.add.open {
		t.Fatal("the form stayed open after every link was added")
	}
}

func TestPasteTorrentFile(t *testing.T) {
	tor := filepath.Join(t.TempDir(), "debian.torrent")
	if err := os.WriteFile(tor, []byte("d4:infoe"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := sampleDaemon()
	u := startTUI(t, options(t, d), 120, 32)
	u.waitFor(t, "aria2")
	u.Send(tea.PasteMsg{Content: tor})
	u.waitFor(t, titleAdd)
	u.Send(key("enter"))
	waitCalls(t, d, "add torrent(8 bytes) dir=/dl out= paused=false force=false")
	u.quit(t)
}

func TestPasteWhileDisconnectedToasts(t *testing.T) {
	m := update(New(options(t)), tea.WindowSizeMsg{Width: 120, Height: 32}, tea.PasteMsg{Content: pasted})
	if m.add.open || m.toast.text != textNotConnected {
		t.Fatalf("form open %v, toast %q", m.add.open, m.toast.text)
	}
}

func TestPasteOnButtonGoesToLink(t *testing.T) {
	m := update(sampleModel(t, 120, 32), key("a"), key("tab"), key("tab"), key("tab"), key("tab"), tea.PasteMsg{Content: "https://e.example/x"})
	if got := m.add.fields[stopLink].Value(); got != "https://e.example/x" || m.add.focus != stopLink {
		t.Fatalf("link %q, focus %d", got, m.add.focus)
	}
}
