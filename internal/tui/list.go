package tui

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

const (
	barCells  = 24
	tabProbIx = 3
)

// tabDef is one view of the list. Its states match the daemon summary's
// buckets, so its count is the summary's.
type tabDef struct {
	name   string
	states []store.State
	empty  string
}

var tabs = [4]tabDef{
	{tabDownloading, []store.State{store.Active}, emptyDownloading},
	{tabWaiting, []store.State{store.Queued, store.Paused, store.RetryWait}, emptyWaiting},
	{tabFinished, []store.State{store.Done, store.Seeding}, emptyFinished},
	{tabProblems, []store.State{store.Error}, emptyProblems},
}

// tabOf is the tab that lists jobs in state s.
func tabOf(s store.State) int {
	for i, t := range tabs {
		if slices.Contains(t.states, s) {
			return i
		}
	}
	return 0
}

func (m Model) tabCount(i int) int {
	s := m.summary
	return [4]int{s.Active, s.Waiting, s.Done + s.Seeding, s.Attention}[i]
}

// tabsShown is 4 while something failed, else 3: Problems only exists
// when there are problems.
func (m Model) tabsShown() int {
	if m.summary.Attention > 0 {
		return 4
	}
	return 3
}

// rows are the current tab's jobs, in the daemon's order.
func (m Model) rows() []*store.Job {
	var out []*store.Job
	for _, j := range m.jobs {
		if slices.Contains(tabs[m.tab].states, j.State) {
			out = append(out, j)
		}
	}
	return out
}

// tabsLine draws the tabs with counts, and the rule under them with a
// heavy part under the active tab. It returns each tab's x and width.
func (m Model) tabsLine(w int) (line, rule string, spans [][2]int) {
	t := m.theme
	x := 2
	var b strings.Builder
	b.WriteString("  ")
	ruleB := strings.Builder{}
	ruleB.WriteString("  ")
	for i := range m.tabsShown() {
		label := tabs[i].name + " " + strconv.Itoa(m.tabCount(i))
		lw := lipgloss.Width(label)
		st := lipgloss.NewStyle()
		if i == tabProbIx {
			st = t.bad
		}
		if i == m.tab {
			st = st.Bold(true)
			ruleB.WriteString(t.accent.Render(strings.Repeat(t.G.tabOn, lw)))
		} else {
			ruleB.WriteString(t.muted.Render(strings.Repeat(t.G.tabOff, lw)))
		}
		b.WriteString(st.Render(label))
		spans = append(spans, [2]int{x, lw})
		gap := "   "
		b.WriteString(gap)
		ruleB.WriteString(t.muted.Render(strings.Repeat(t.G.tabOff, len(gap))))
		x += lw + len(gap)
	}
	if rest := w - 2 - x; rest > 0 {
		ruleB.WriteString(t.muted.Render(strings.Repeat(t.G.tabOff, rest)))
	}
	return fit(b.String(), w), fit(ruleB.String(), w), spans
}

type rowOpts struct {
	selected, hover bool
	numW            int // cells for "#14"; 0 hides numbers
	si              bool
	now             time.Time
}

// renderRow draws one job in width cells: two lines, or one when compact.
// Line one: selection mark, icon, number, name and size. Line two: the
// bar and percent with speed and time left, or the daemon's label.
func renderRow(t Theme, j *store.Job, width int, o rowOpts, compact bool) string {
	mark := " "
	if o.selected {
		mark = t.accent.Render(t.G.sel)
	}
	name := j.Name
	nameSt := lipgloss.NewStyle()
	if o.selected || o.hover {
		nameSt = nameSt.Bold(true)
	}
	pre := mark + " " + t.Icon(j.State) + " "
	if o.numW > 0 {
		pre += t.muted.Render(fmt.Sprintf("%-*s", o.numW, fmt.Sprintf(fmtNum, j.Num))) + " "
	}
	if compact {
		tail := ""
		if p, ok := percent(j); ok {
			tail = fmt.Sprintf(fmtPercent, p)
		} else {
			tail = t.role(j.State).Render(j.Label)
		}
		room := width - lipgloss.Width(pre) - lipgloss.Width(tail) - 2
		return fit(pre+nameSt.Render(midTrunc(name, room, t.G.ellipsis))+"  ", width-lipgloss.Width(tail)-1) + tail + " "
	}
	size := t.muted.Render(sizeText(j, o.si))
	room := width - lipgloss.Width(pre) - lipgloss.Width(size) - 3
	line1 := pre + nameSt.Render(midTrunc(name, room, t.G.ellipsis))
	line1 = fit(line1, width-lipgloss.Width(size)-1) + size + " "

	var parts []string
	if p, ok := percent(j); ok && j.State != store.Done && j.State != store.Seeding {
		parts = append(parts, bar(t, j.State, float64(p)/100, barCells), fmt.Sprintf("%4s", fmt.Sprintf(fmtPercent, p)))
	}
	switch j.State {
	case store.Active:
		if len(parts) == 0 { // the size is not known yet: say why there is no bar
			parts = append(parts, t.accent.Render(j.Label))
		}
		if j.SpeedDown > 0 {
			parts = append(parts, fmt.Sprintf("%10s", fmt.Sprintf(fmtSpeed, textutil.Bytes(j.SpeedDown, o.si))))
		}
		if j.ETASeconds != nil {
			parts = append(parts, t.muted.Render(fmt.Sprintf(fmtLeft, textutil.Duration(time.Duration(*j.ETASeconds)*time.Second))))
		}
	case store.Done:
		when := ""
		if j.FinishedAt != nil {
			when = " " + ago(o.now, *j.FinishedAt)
		}
		parts = append(parts, t.ok.Render(j.Label)+t.muted.Render(when))
	case store.Seeding:
		parts = append(parts, t.ok.Render(j.Label), t.muted.Render(t.G.up+" "+fmt.Sprintf(fmtSpeed, textutil.Bytes(j.SpeedUp, o.si))))
	default:
		parts = append(parts, t.role(j.State).Render(j.Label))
	}
	line2 := mark + "  " + strings.Join(parts, "   ")
	return line1 + "\n" + fit(line2, width)
}

// percent is the job's progress when its size is known.
func percent(j *store.Job) (int, bool) {
	if j.TotalBytes == nil || *j.TotalBytes <= 0 {
		return 0, false
	}
	done := int64(0)
	if j.DoneBytes != nil {
		done = *j.DoneBytes
	}
	return int(min(done*100 / *j.TotalBytes, 100)), true
}

// sizeText is "4.7 of 6.0 GiB" while running, the size when finished.
func sizeText(j *store.Job, si bool) string {
	var done, total int64
	if j.DoneBytes != nil {
		done = *j.DoneBytes
	}
	if j.TotalBytes != nil {
		total = *j.TotalBytes
	}
	switch {
	case total <= 0 && done <= 0:
		return ""
	case total <= 0:
		return textutil.Bytes(done, si)
	case j.State == store.Done || j.State == store.Seeding || done >= total:
		return textutil.Bytes(total, si)
	}
	d, tt := textutil.Bytes(done, si), textutil.Bytes(total, si)
	if du, tu := unit(d), unit(tt); du == tu && du != "B" {
		d = strings.TrimSuffix(d, " "+du)
	}
	return fmt.Sprintf(fmtOf, d, tt)
}

func unit(s string) string { return s[strings.LastIndexByte(s, ' ')+1:] }

// bar draws frac of width cells in the state's colour, with an eighth
// cell at the edge when the glyph set has them.
func bar(t Theme, s store.State, frac float64, width int) string {
	steps := 1
	if t.G.eighths != nil {
		steps = 8
	}
	n := int(math.Round(min(max(frac, 0), 1) * float64(width*steps)))
	full, part := n/steps, n%steps
	filled := strings.Repeat(t.G.full, full)
	if part > 0 {
		filled += t.G.eighths[part-1]
		full++
	}
	return t.role(s).Render(filled) + t.muted.Render(strings.Repeat(t.G.empty, width-full))
}

// midTrunc cuts the middle of a name so its end (the extension) stays.
func midTrunc(s string, w int, ellipsis string) string {
	if w <= 0 {
		return ""
	}
	n := lipgloss.Width(s)
	if n <= w {
		return s
	}
	tail := (w - 1) / 3
	return ansi.Truncate(s, w-1-tail, "") + ellipsis + ansi.TruncateLeft(s, n-tail, "")
}

func truncate(s string, w int, tail string) string { return ansi.Truncate(s, w, tail) }

// ago says how long ago at was, in plain words.
func ago(now, at time.Time) string {
	d := now.Sub(at)
	if d < time.Minute {
		return textJustNow
	}
	return fmt.Sprintf(fmtAgo, textutil.Duration(d))
}

// listRows draws the list pane's rows from the top one shown.
func (m Model) listRows(l layout, rows []*store.Job, top int) []string {
	t := m.theme
	inner := l.list.w - 2
	o := rowOpts{si: m.o.Settings.UI.Units == "si", now: m.now()}
	if l.wide {
		o.numW = len(fmt.Sprintf(fmtNum, maxNum(rows)))
	}
	sel := m.cursor()
	var out []string
	for i := top; i < len(rows) && i < top+l.visibleRows(); i++ {
		o.selected = i == sel
		o.hover = m.hover == layerRow+rows[i].ID
		out = append(out, renderRow(t, rows[i], inner, o, l.compact))
	}
	return out
}

func maxNum(js []*store.Job) int64 {
	var n int64
	for _, j := range js {
		n = max(n, j.Num)
	}
	return n
}

// emptyView is the empty tab's one sentence and one key, centred.
func (m Model) emptyView(w, h int) string {
	t := m.theme
	lines := []string{tabs[m.tab].empty, keyLine(t, fmtEmptyBack, emptyBackKey)}
	if m.tab == 0 {
		lines = []string{lines[0], keyLine(t, fmtEmptyAdd, emptyAddKey), "", t.muted.Render(emptyKeepGoing)}
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, lipgloss.JoinVertical(lipgloss.Center, lines...))
}

// keyLine is a muted sentence with its one %s, a key, in bold.
func keyLine(t Theme, format, key string) string {
	before, after, _ := strings.Cut(format, "%s")
	return t.muted.Render(before) + t.bold.Render(key) + t.muted.Render(after)
}
