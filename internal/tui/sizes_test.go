package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Every screen and overlay draws at any size, down to 1x1, without a panic.
func TestTinySizesDraw(t *testing.T) {
	opens := map[string][]tea.Msg{
		"list":   nil,
		"help":   {key("?")},
		"remove": {key("x")},
		"add":    {key("a")},
		"dup":    {key("a"), tea.PasteMsg{Content: "https://e.example/a.iso"}, addMsg{dup: &store.Job{ID: "x", Num: 3}}},
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

// Every footer key is clickable exactly where it is drawn, also when the
// keys are wider than the terminal.
func TestFooterKeysUnderClick(t *testing.T) {
	for _, w := range append([]int{80, 120}, rangeInts(20, 60)...) {
		m := sampleModel(t, w, 24)
		l := m.layout()
		c := m.compose()
		line := ansi.Strip(strings.Split(c.Render(), "\n")[l.footY])
		labels := map[string]string{}
		for _, h := range m.hints() {
			labels[layerBtn+h.action] = h.key + " " + h.label
		}
		_, areas := m.footer(l)
		for x := range w {
			kind, id := hit(c, x, l.footY)
			if kind != "btn" {
				continue
			}
			i := slices.IndexFunc(areas, func(a area) bool { return a.id == layerBtn+id })
			a := areas[i]
			if drawn := ansi.Cut(line, a.x, a.x+a.w); drawn != labels[a.id] || a.x+a.w > w {
				t.Fatalf("%d wide: a click at %d runs %s, but %q is drawn there", w, x, id, drawn)
			}
		}
	}
}

func rangeInts(lo, hi int) []int {
	var out []int
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
}
