package tui

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

const (
	detailsDebounce = 150 * time.Millisecond // after the selection moves
	detailsEvery    = time.Second            // at most this often while a job runs
	pieceRows       = 2
	detailFiles     = 5
	detailEvents    = 5
)

type (
	detailsMsg struct {
		gen int
		id  string
		d   daemon.Details
		err error
	}
	detailsTickMsg struct{ seq int }
)

// watchDetails keeps the details of the selected job fresh while the pane
// is visible: shortly after the selection moves, and when a frame or a
// snapshot touches the job.
// shortcut: details poll at most 1 Hz while visible, upgrade to a details push if it costs too much
func (m *Model) watchDetails(msg tea.Msg) tea.Cmd {
	j := m.selected()
	if j == nil || m.d == nil || m.layout().details.w == 0 {
		m.detID, m.detDue = "", false
		return nil
	}
	if j.ID != m.detID {
		m.detID, m.detDue, m.detTop = j.ID, true, 0
		m.detSeq++
		seq := m.detSeq
		return tea.Tick(detailsDebounce, func(time.Time) tea.Msg { return detailsTickMsg{seq} })
	}
	switch msg := msg.(type) {
	case detailsTickMsg:
		if msg.seq == m.detSeq {
			m.detDue = false
			return m.fetchDetails()
		}
	case frameMsg:
		if slices.ContainsFunc(msg.f.Items, func(it daemon.ProgressItem) bool { return it.ID == j.ID }) {
			return m.refreshDetails()
		}
	case snapshotMsg:
		return m.refreshDetails()
	}
	return nil
}

// refreshDetails fetches now when the last fetch is old enough, else once
// it is. A fetch still running is left to finish.
func (m *Model) refreshDetails() tea.Cmd {
	if m.detDue || m.detBusy {
		return nil
	}
	wait := detailsEvery - m.now().Sub(m.detAt)
	if wait <= 0 {
		return m.fetchDetails()
	}
	m.detDue = true
	seq := m.detSeq
	return tea.Tick(wait, func(time.Time) tea.Msg { return detailsTickMsg{seq} })
}

func (m *Model) fetchDetails() tea.Cmd {
	m.detAt, m.detBusy = m.now(), true
	gen, d, id, cells := m.gen, m.d, m.detID, max(m.layout().details.w-4, 1)*pieceRows
	return m.cmd(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		r, err := d.Details(ctx, id, cells)
		return detailsMsg{gen, id, r, err}
	})
}

// gotDetails keeps a reply for the job still selected.
func (m *Model) gotDetails(msg detailsMsg) {
	m.detBusy = false
	if msg.gen == m.gen && msg.err == nil && msg.id == m.detID {
		m.det = msg.d
	}
}

// selectedDetails is the last reply for the selected job with the job as
// the list has it now, which frames keep current.
func (m Model) selectedDetails() daemon.Details {
	j := m.selected()
	d := m.det
	if j == nil || d.Job == nil || d.Job.ID != j.ID {
		d = daemon.Details{}
	}
	d.Job = j
	return d
}

type detailOpts struct {
	width int // cells for the text, padding excluded
	si    bool
	links bool // the folder is an OSC 8 link
	now   time.Time
}

// renderDetails is the details pane's text: what the job is, where it
// goes, where it comes from, the piece map, connections, files and what
// happened.
func renderDetails(t Theme, d daemon.Details, o detailOpts) string {
	j := d.Job
	if j == nil || o.width < 1 {
		return ""
	}
	w := o.width
	var lines []string
	add := func(s ...string) { lines = append(lines, s...) }
	head := func(s string) { add("", t.muted.Render(s)) }

	add(t.bold.Render(midTrunc(j.Name, w, t.G.ellipsis)))
	facts := []string{fmt.Sprintf(fmtNum, j.Num), t.role(j.State).Render(t.G.icon(j.State) + " " + j.Label)}
	if p, ok := percent(j); ok && j.State != store.Done && j.State != store.Seeding {
		facts = append(facts, fmt.Sprintf(fmtPercent, p))
	}
	add(truncate(strings.Join(facts, " · "), w, t.G.ellipsis))
	if s := sizeText(j, o.si); s != "" {
		add(s)
	}
	if j.State == store.Active && j.SpeedDown > 0 {
		s := fmt.Sprintf(fmtSpeed, textutil.Bytes(j.SpeedDown, o.si))
		if j.ETASeconds != nil {
			s += ", " + fmt.Sprintf(fmtLeft, textutil.Duration(time.Duration(*j.ETASeconds)*time.Second))
		}
		add(s)
	}
	if j.ErrorMsg != nil {
		add(t.bad.Render(truncate(*j.ErrorMsg, w, t.G.ellipsis)))
	}

	head(headSavedTo)
	dir := truncate(j.Dir, w, t.G.ellipsis)
	if o.links {
		dir = lipgloss.NewStyle().Hyperlink((&url.URL{Scheme: "file", Path: j.Dir}).String()).Render(dir)
	}
	add(dir)
	if len(j.URIs) > 0 {
		head(headFrom)
		add(midTrunc(textutil.StripUserinfo(j.URIs[0], "***@"), w, t.G.ellipsis))
	}

	if len(d.Segments) > 0 {
		head(headPieces)
		for i := 0; i < len(d.Segments) && i < pieceRows*w; i += w {
			add(pieceRow(t, d.Segments[i:min(i+w, len(d.Segments))]))
		}
		g := t.G.pieces
		add(truncate(t.ok.Render(g[0])+" "+t.muted.Render(legendDone)+"  "+t.accent.Render(g[1])+" "+t.muted.Render(legendNow)+
			"  "+t.muted.Render(g[2]+" "+legendNotYet), w, ""))
	}

	if len(d.Servers) > 0 || d.Peers > 0 {
		head(headConnections)
		hs := hostCounts(d.Servers)
		hw := 0
		for _, h := range hs {
			hw = max(hw, lipgloss.Width(h.host))
		}
		for _, h := range hs {
			add(truncate(fmt.Sprintf("%-*s  %s", hw, h.host, plural(h.n, fmtConnOne, fmtConnMany)), w, t.G.ellipsis))
		}
		if d.Peers > 0 {
			add(plural(d.Peers, fmtPeerOne, fmtPeerMany))
		}
	}

	if j.Kind == "torrent" && len(d.Files) > 0 {
		head(headFiles)
		for _, f := range d.Files[:min(len(d.Files), detailFiles)] {
			pct := 0
			if f.Length > 0 {
				pct = int(f.Done * 100 / f.Length)
			}
			name := f.Path[strings.LastIndexByte(f.Path, '/')+1:]
			add(truncate(fmt.Sprintf("%4s  %9s  %s", fmt.Sprintf(fmtPercent, pct), textutil.Bytes(f.Length, o.si), name), w, t.G.ellipsis))
		}
		if n := len(d.Files) - detailFiles; n > 0 {
			add(t.muted.Render(fmt.Sprintf(fmtMoreFiles, n)))
		}
	}

	if len(d.Events) > 0 {
		head(headHistory)
		for _, e := range d.Events[max(len(d.Events)-detailEvents, 0):] {
			s := t.muted.Render(fmt.Sprintf("%-11s", ago(o.now, e.At))) + " " + strings.ReplaceAll(e.Kind, "-", " ")
			if e.Detail != "" {
				s += t.muted.Render(" " + e.Detail)
			}
			add(truncate(s, w, t.G.ellipsis))
		}
	}
	return strings.Join(lines, "\n")
}

// pieceCells draws one cell per segment: done, started or not yet.
func pieceCells(segments []int, g Glyphs) string {
	var b strings.Builder
	for _, s := range segments {
		b.WriteString(g.pieces[pieceKind(s)])
	}
	return b.String()
}

func pieceKind(s int) int {
	switch {
	case s >= 100:
		return 0
	case s > 0:
		return 1
	}
	return 2
}

// pieceRow colours runs of cells: done green, started blue, the rest muted.
func pieceRow(t Theme, segments []int) string {
	styles := [3]lipgloss.Style{t.ok, t.accent, t.muted}
	var b strings.Builder
	for i := 0; i < len(segments); {
		k, n := pieceKind(segments[i]), 1
		for i+n < len(segments) && pieceKind(segments[i+n]) == k {
			n++
		}
		b.WriteString(styles[k].Render(pieceCells(segments[i:i+n], t.G)))
		i += n
	}
	return b.String()
}

type hostCount struct {
	host string
	n    int
}

// hostCounts groups the server links by host, in the order first seen.
func hostCounts(servers []string) []hostCount {
	var out []hostCount
	for _, s := range servers {
		h := s
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			h = u.Hostname()
		}
		if i := slices.IndexFunc(out, func(c hostCount) bool { return c.host == h }); i >= 0 {
			out[i].n++
		} else {
			out = append(out, hostCount{h, 1})
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}

// detailsView is the pane's lines from the scroll offset on.
func (m Model) detailsView(l layout) string {
	lines := strings.Split(m.detailsText(l), "\n")
	return strings.Join(lines[min(m.detTop, len(lines)-1):], "\n")
}

// detailsText is all of the pane's lines, padded.
func (m Model) detailsText(l layout) string {
	d := m.selectedDetails()
	if d.Job == nil {
		return ""
	}
	s := renderDetails(m.theme, d, detailOpts{width: l.details.w - 4, si: m.o.Settings.UI.Units == "si",
		links: m.o.Settings.UI.OSCLinks && !m.dumb, now: m.now()})
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	return strings.Join(lines, "\n")
}
