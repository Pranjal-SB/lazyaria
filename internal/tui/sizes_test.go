package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Every screen and overlay draws at any size, down to 1x1, without a panic.
func TestTinySizesDraw(t *testing.T) {
	t.Parallel()
	opens := map[string][]tea.Msg{
		"list":   nil,
		"help":   {key("?")},
		"remove": {key("x")},
		"add":    {key("a")},
		"dup":    {key("a"), tea.PasteMsg{Content: "https://e.example/a.iso"}, addMsg{dup: &store.Job{ID: "x", Num: 3}}},
	}
	for name, msgs := range opens {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Model is a value: every size starts from the same opened screen.
			base := update(sampleModel(t, 120, 32), msgs...)
			base.o.Now = func() time.Time { return goldenNow }
			before := base.View().Content
			for w := 1; w <= 20; w++ {
				for h := 1; h <= 5; h++ {
					t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
						m := update(base, tea.WindowSizeMsg{Width: w, Height: h})
						m.View()
						update(m, tea.MouseMotionMsg{X: w / 2, Y: h / 2}, tea.MouseClickMsg{X: w / 2, Y: h / 2, Button: tea.MouseLeft})
					})
				}
			}
			if base.View().Content != before {
				t.Fatal("a size's run changed the shared screen it started from")
			}
		})
	}
}

// On a terminal too short for the footer, its keys cannot be clicked, and
// a double click that opens the remove dialog never presses one of its
// buttons, at any width.
func TestDoubleClickNeverDeletes(t *testing.T) {
	t.Parallel()
	for _, h := range []int{5, 24} {
		for w := 66; w <= 86; w++ {
			t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
				t.Parallel()
				m := sampleModel(t, w, h)
				footer, tested := m.layout().footY < h, 0
				press := func(x, y int) Model {
					return update(m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}, tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
				}
				for _, l := range m.layers() {
					if !strings.HasPrefix(l.GetID(), layerBtn) { // only a footer key can open the dialog
						continue
					}
					if !footer {
						t.Fatalf("footer key %s is clickable with no footer drawn", l.GetID())
					}
					// A key runs the same action from any of its cells, so
					// one click tells whether it opens the dialog at all.
					if !press(l.GetX(), l.GetY()).remove.open() {
						tested++
						continue
					}
					for y := l.GetY(); y < l.GetY()+l.Height(); y++ {
						for x := l.GetX(); x < l.GetX()+l.Width(); x++ {
							tested++
							c := press(x, y)
							if !c.remove.open() {
								t.Fatalf("%s opened the dialog from one cell but not from %d,%d", l.GetID(), x, y)
							}
							kind, id := hit(c.compose(), x, y)
							if kind != "modal" || id == "" {
								continue
							}
							if r := update(c, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}); !r.remove.open() {
								t.Fatalf("a double click at %d,%d pressed the dialog's %s button", x, y, id)
							}
						}
					}
				}
				if footer && tested == 0 {
					t.Fatal("the footer is drawn but none of its keys was clicked")
				}
			})
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
