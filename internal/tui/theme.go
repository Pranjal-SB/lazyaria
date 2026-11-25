package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Glyphs is one glyph set (ui.glyphs). Every glyph is one cell wide.
type Glyphs struct {
	icons                   map[store.State]string
	full, empty             string   // progress bar cells
	eighths                 []string // partial cells, 1/8 to 7/8; nil: whole cells only
	pieces                  [3]string
	spark                   []string // lowest to highest
	sel, ellipsis           string
	dotOK, dotWait, dotFail string
	tabOn, tabOff           string // the rule under the active tab, and elsewhere
	down, up                string // speed arrows in the top bar
	arrow                   string // "to", in a button label; may be wider than one cell
	border                  lipgloss.Border
}

var (
	unicodeGlyphs = Glyphs{
		icons: map[store.State]string{store.Active: "↓", store.Queued: "○", store.RetryWait: "○", store.Paused: "‖",
			store.Error: "✗", store.Seeding: "↑", store.Done: "✓", store.Removed: "✗"},
		full: "█", empty: "░", eighths: []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉"},
		pieces: [3]string{"█", "▒", "░"},
		spark:  []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"},
		sel:    "▌", ellipsis: "…",
		dotOK: "●", dotWait: "◐", dotFail: "✗",
		tabOn: "━", tabOff: "─", down: "↓", up: "↑", arrow: "→",
		border: lipgloss.RoundedBorder(),
	}
	asciiGlyphs = Glyphs{
		icons: map[store.State]string{store.Active: "v", store.Queued: "o", store.RetryWait: "o", store.Paused: "=",
			store.Error: "x", store.Seeding: "^", store.Done: "+", store.Removed: "x"},
		full: "#", empty: "-",
		pieces: [3]string{"#", "+", "."},
		spark:  []string{"_", ".", ":", "-", "=", "+", "*", "#"},
		sel:    ">", ellipsis: "~",
		dotOK: "*", dotWait: "~", dotFail: "x",
		tabOn: "=", tabOff: "-", down: "v", up: "^", arrow: "->",
		border: lipgloss.Border{Top: "-", Bottom: "-", Left: "|", Right: "|", TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+"},
	}
	// nerdGlyphs needs a Nerd Font (Font Awesome range); the rest is unicode.
	nerdGlyphs = func() Glyphs {
		g := unicodeGlyphs
		g.icons = map[store.State]string{store.Active: "\uf019", store.Queued: "\uf017", store.RetryWait: "\uf021",
			store.Paused: "\uf04c", store.Error: "\uf00d", store.Seeding: "\uf093", store.Done: "\uf00c", store.Removed: "\uf00d"}
		return g
	}()
)

// Theme is the glyphs and the colour roles. Colours are the 16 ANSI ones,
// foreground only, so the terminal's palette decides how they look; the
// renderer drops them for NO_COLOR.
type Theme struct {
	G                                  Glyphs
	accent, ok, warn, bad, muted, bold lipgloss.Style
}

func NewTheme(glyphs string) Theme {
	g := unicodeGlyphs
	switch glyphs {
	case "ascii":
		g = asciiGlyphs
	case "nerd":
		g = nerdGlyphs
	}
	s := lipgloss.NewStyle()
	return Theme{G: g,
		accent: s.Foreground(lipgloss.Blue), ok: s.Foreground(lipgloss.Green), warn: s.Foreground(lipgloss.Yellow),
		bad: s.Foreground(lipgloss.Red), muted: s.Foreground(lipgloss.BrightBlack), bold: s.Bold(true)}
}

func (g Glyphs) icon(s store.State) string { return g.icons[s] }

// role is the colour of a state: blue moving, yellow waiting, green
// finished, red failed.
func (t Theme) role(s store.State) lipgloss.Style {
	switch s {
	case store.Active:
		return t.accent
	case store.Queued, store.Paused, store.RetryWait:
		return t.warn
	case store.Done, store.Seeding:
		return t.ok
	}
	return t.bad
}

// Icon is the state's glyph in its colour.
func (t Theme) Icon(s store.State) string { return t.role(s).Render(t.G.icon(s)) }

func cellWidth(s string) int { return ansi.StringWidth(s) }
