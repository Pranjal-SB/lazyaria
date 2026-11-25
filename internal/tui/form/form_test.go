package form

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestButtonsMoveWraps(t *testing.T) {
	b := Buttons{Labels: []string{"a", "b", "c"}}
	for _, step := range []struct{ delta, want int }{{-1, 2}, {1, 0}, {1, 1}, {4, 2}} {
		b.Move(step.delta)
		if b.Focus != step.want {
			t.Fatalf("Move(%d) focus %d, want %d", step.delta, b.Focus, step.want)
		}
	}
	s, spans := b.View(func(int, bool) lipgloss.Style { return lipgloss.NewStyle() })
	if s != "[ a ]  [ b ]  [ c ]" || spans[1] != [2]int{7, 5} {
		t.Fatalf("view %q spans %v", s, spans)
	}
}

func TestFieldTakesKeysAndPastes(t *testing.T) {
	f := NewField("Link", "", "", lipgloss.NewStyle())
	f.Focus()
	f, _ = f.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	f, _ = f.Update(tea.PasteMsg{Content: " y"})
	if f.Value() != "x y" {
		t.Fatalf("value %q", f.Value())
	}
	f.Blur()
	if f, _ = f.Update(tea.KeyPressMsg{Code: 'z', Text: "z"}); f.Value() != "x y" {
		t.Fatalf("a blurred field took a key: %q", f.Value())
	}
}
