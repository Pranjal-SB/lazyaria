package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Every screen and overlay draws at any size, down to 1x1, without a panic.
func TestTinySizesDraw(t *testing.T) {
	opens := map[string][]tea.Msg{
		"list":   nil,
		"help":   {key("?")},
		"remove": {key("x")},
		"add":    {key("a")},
		"dup":    {key("a"), tea.PasteMsg{Content: "https://e.example/a.iso"}, addMsg{err: &daemon.DuplicateError{Existing: &store.Job{ID: "x", Num: 3}}}},
	}
	for name, msgs := range opens {
		for w := 1; w <= 20; w++ {
			for h := 1; h <= 5; h++ {
				t.Run(fmt.Sprintf("%s_%dx%d", name, w, h), func(t *testing.T) {
					m := update(sampleModel(t, 120, 32), msgs...)
					m = update(m, tea.WindowSizeMsg{Width: w, Height: h})
					m.View()
					update(m, tea.MouseMotionMsg{X: w / 2, Y: h / 2}, tea.MouseClickMsg{X: w / 2, Y: h / 2, Button: tea.MouseLeft})
				})
			}
		}
	}
}

// On a terminal too short for the footer, its keys cannot be clicked, and
// a double click that opens the remove dialog never presses one of its
// buttons, at any width.
func TestDoubleClickNeverDeletes(t *testing.T) {
	for _, h := range []int{5, 24} {
		for w := 66; w <= 86; w++ {
			m := sampleModel(t, w, h)
			screen := m.compose()
			for y := range h {
				for x := range w {
					if kind, _ := hit(screen, x, y); kind != "btn" { // only a footer key can open the dialog
						continue
					}
					c := update(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}, tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
					if !c.remove.open() {
						continue
					}
					kind, id := hit(c.compose(), x, y)
					if kind != "modal" || id == "" {
						continue
					}
					if r := update(c, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); !r.remove.open() {
						t.Fatalf("%dx%d: a double click at %d,%d pressed the dialog's %s button", w, h, x, y, id)
					}
				}
			}
		}
	}
}
