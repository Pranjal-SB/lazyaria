package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

const sparkCells = 12

// topBar is one line: speeds with the sparkline, time to finish all and
// the engine dot with a word. Narrow terminals drop the ETA, then the
// sparkline.
func (m Model) topBar(w int) string {
	t := m.theme
	si := m.o.Settings.UI.Units == "si"
	s := m.summary
	down := t.accent.Render(t.G.down) + " " + fmt.Sprintf(fmtSpeed, textutil.Bytes(s.Down, si))
	spark := t.accent.Render(sparkline(t.G, m.speeds, sparkCells))
	up := t.muted.Render(t.G.up + " " + fmt.Sprintf(fmtSpeed, textutil.Bytes(s.Up, si)))
	eta := ""
	if s.ETASeconds != nil {
		eta = t.muted.Render(fmt.Sprintf(fmtFinishAll, textutil.Duration(time.Duration(*s.ETASeconds)*time.Second)))
	}
	right := m.engineDot() + " "
	for _, left := range []string{
		" " + t.bold.Render(textApp) + "   " + down + " " + spark + "   " + up,
		" " + t.bold.Render(textApp) + "   " + down + "   " + up,
		" " + t.bold.Render(textApp) + "   " + down,
	} {
		r := right
		if eta != "" {
			r = eta + "     " + right
		}
		for _, r := range []string{r, right} {
			if gap := w - lipgloss.Width(left) - lipgloss.Width(r); gap >= 2 {
				return left + strings.Repeat(" ", gap) + r
			}
		}
	}
	return fit(" "+t.bold.Render(textApp)+"  "+right, w)
}

// engineDot is the connection and engine state: an icon and a word.
func (m Model) engineDot() string {
	t := m.theme
	switch {
	case m.d == nil && m.everUp:
		return t.warn.Render(t.G.dotWait) + " " + textReconnecting
	case m.d == nil:
		return t.warn.Render(t.G.dotWait) + " " + textConnecting
	}
	switch m.status.EngineState {
	case supervisor.PhaseReady:
		return t.ok.Render(t.G.dotOK) + " " + textEngine + " " + textEngineOK
	case supervisor.PhaseFailed:
		return t.bad.Render(t.G.dotFail) + " " + textEngine + " " + m.status.EngineState
	}
	return t.warn.Render(t.G.dotWait) + " " + textEngine + " " + m.status.EngineState
}

// sparkline draws the last samples in at most width cells, scaled to the
// highest. With more samples than cells each cell shows its bucket's peak.
func sparkline(g Glyphs, samples []int64, width int) string {
	n := min(len(samples), width)
	if n == 0 {
		return ""
	}
	cells := make([]int64, n)
	for i := range cells {
		lo, hi := i*len(samples)/n, (i+1)*len(samples)/n
		for _, v := range samples[lo:hi] {
			cells[i] = max(cells[i], v)
		}
	}
	var top int64
	for _, v := range cells {
		top = max(top, v)
	}
	var b strings.Builder
	levels := int64(len(g.spark) - 1)
	for _, v := range cells {
		i := int64(0)
		if top > 0 {
			i = (v*levels + top/2) / top
		}
		b.WriteString(g.spark[i])
	}
	return b.String()
}
