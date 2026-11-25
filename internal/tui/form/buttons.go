package form

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Buttons is a row of buttons. Focus is the focused one, or -1.
type Buttons struct {
	Labels []string
	Focus  int
}

// View draws the row with style(i) for button i, and returns each
// button's x and width for hit testing.
func (b Buttons) View(style func(i int, focused bool) lipgloss.Style) (string, [][2]int) {
	var parts []string
	var spans [][2]int
	x := 0
	for i, l := range b.Labels {
		s := style(i, i == b.Focus).Render("[ " + l + " ]")
		parts = append(parts, s)
		spans = append(spans, [2]int{x, lipgloss.Width(s)})
		x += lipgloss.Width(s) + 2
	}
	return strings.Join(parts, "  "), spans
}

// Move moves the focus by delta, wrapping around.
func (b *Buttons) Move(delta int) {
	n := len(b.Labels)
	b.Focus = ((b.Focus+delta)%n + n) % n
}
