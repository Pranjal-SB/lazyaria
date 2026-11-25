// Package tui is the terminal interface: a thin client of the daemon. It
// formats and lays out what the daemon sends and never derives download
// facts itself. Every daemon call runs in a tea.Cmd.
package tui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Model is the whole TUI state.
type Model struct {
	o     Options
	theme Theme

	gen      int // connection generation; bumped on every disconnect
	d        Daemon
	ev       <-chan daemon.Event
	fr       <-chan daemon.Frame
	everUp   bool
	failures int // connects failed in a row
	lastErr  error
	backoff  time.Duration
	pending  bool // a debounced re-fetch is scheduled

	jobs    []*store.Job
	seq     uint64 // the applied snapshot's bus sequence
	summary daemon.Summary
	status  ipc.Status
	speeds  []int64 // download speed samples for the sparkline, oldest first

	w, h        int
	tab         int
	sel         [len(tabs)]string // the selected job's id, per tab
	selIx       [len(tabs)]int    // its row, kept when the job leaves the tab
	top         [len(tabs)]int    // the first row shown, per tab
	showDetails bool              // narrow layouts: the pane under the list is open
	hover       string            // the layer under the pointer
	flash       string            // the last action's error
}

func New(o Options) Model { return Model{o: o, theme: NewTheme(o.Settings.UI.Glyphs)} }

func (m Model) Init() tea.Cmd { return m.connect() }

func (m Model) cmd(c tea.Cmd) tea.Cmd { return guard(c, m.o.CrashDir) }

func (m Model) now() time.Time {
	if m.o.Now != nil {
		return m.o.Now()
	}
	return time.Now()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer logPanic(m.o.CrashDir)
	m, cmd := m.update(msg)
	m.syncSel()
	return m, cmd
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.EnvMsg:
		if msg.Getenv("TERM") == "dumb" {
			m.theme = NewTheme("ascii")
		}
	case connectedMsg:
		if msg.gen != m.gen {
			return m, m.closeCmd(msg.d)
		}
		m.d, m.ev, m.fr, m.status = msg.d, msg.ev, msg.fr, msg.status
		m.everUp, m.failures, m.lastErr, m.backoff = true, 0, nil, 0
		m.setSnapshot(msg.snap)
		m.speeds = nil
		for _, s := range msg.snap.Speed {
			m.addSpeed(s.Down)
		}
		return m, tea.Batch(m.waitEvent(), m.waitFrame())
	case disconnectedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		var cmds []tea.Cmd
		if m.d != nil {
			cmds = append(cmds, m.closeCmd(m.d))
		}
		m.gen++
		m.d, m.ev, m.fr, m.pending = nil, nil, nil, false
		m.failures++
		if msg.err != nil {
			m.lastErr = msg.err
		}
		m.backoff = nextBackoff(m.backoff)
		gen := m.gen
		cmds = append(cmds, tea.Tick(m.backoff, func(time.Time) tea.Msg { return retryMsg{gen} }))
		return m, tea.Batch(cmds...)
	case retryMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		return m, m.connect()
	case eventMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		cmds := []tea.Cmd{m.waitEvent()}
		if !m.pending {
			m.pending = true
			gen := m.gen
			cmds = append(cmds, tea.Tick(debounce, func(time.Time) tea.Msg { return debounceMsg{gen} }))
		}
		return m, tea.Batch(cmds...)
	case debounceMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.pending = false
		return m, m.refetch()
	case snapshotMsg:
		// re-fetches can overlap: an older snapshot must not replace a newer one
		if msg.gen == m.gen && msg.err == nil && msg.snap.Seq >= m.seq {
			m.setSnapshot(msg.snap)
		}
	case frameMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.patch(msg.f)
		if len(msg.f.Items) > 0 {
			m.addSpeed(msg.f.Summary.Down)
		}
		return m, m.waitFrame()
	case doneMsg:
		m.flash = ""
		if msg.err != nil {
			m.flash = msg.err.Error()
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyPressMsg) (Model, tea.Cmd) {
	l := m.layout()
	switch a := actionFor(k.String()); a {
	case actQuit:
		return m, tea.Quit
	case actDown:
		m.move(1)
	case actUp:
		m.move(-1)
	case actTop:
		m.move(-len(m.jobs))
	case actBottom:
		m.move(len(m.jobs))
	case actPageUp:
		m.move(-l.visibleRows())
	case actPageDown:
		m.move(l.visibleRows())
	case actTab1, actTab2, actTab3, actTab4:
		if i := int(a[len(a)-1] - '1'); i < m.tabsShown() {
			m.tab = i
		}
	case actNextTab:
		m.tab = (m.tab + 1) % m.tabsShown()
	case actPrevTab:
		m.tab = (m.tab + m.tabsShown() - 1) % m.tabsShown()
	case actDetails:
		if l.narrow() {
			m.showDetails = !m.showDetails
		}
	case actToggle:
		return m, m.toggle()
	}
	return m, nil
}

// toggle pauses or resumes the selected job, whichever the daemon allows.
func (m Model) toggle() tea.Cmd {
	j := m.selected()
	if j == nil || m.d == nil {
		return nil
	}
	ids := []string{j.ID}
	switch {
	case slices.Contains(j.Actions, daemon.ActPause):
		return m.act(daemon.ActPause, ids, func(ctx context.Context, d Daemon) error { return d.Pause(ctx, ids...) })
	case slices.Contains(j.Actions, daemon.ActResume):
		return m.act(daemon.ActResume, ids, func(ctx context.Context, d Daemon) error { return d.Resume(ctx, ids...) })
	}
	return nil
}

// cursor is the selected row of the current tab, or -1 when it is empty.
// The selection follows its job when the daemon reorders the list; when
// the job leaves the tab the row number stays.
func (m Model) cursor() int {
	rows := m.rows()
	if len(rows) == 0 {
		return -1
	}
	if i := slices.IndexFunc(rows, func(j *store.Job) bool { return j.ID == m.sel[m.tab] }); i >= 0 {
		return i
	}
	return min(m.selIx[m.tab], len(rows)-1)
}

func (m Model) selected() *store.Job {
	if i := m.cursor(); i >= 0 {
		return m.rows()[i]
	}
	return nil
}

// move selects the row delta rows away, stopping at the ends.
func (m *Model) move(delta int) {
	rows := m.rows()
	if i := m.cursor(); i >= 0 {
		i = min(max(i+delta, 0), len(rows)-1)
		m.sel[m.tab], m.selIx[m.tab] = rows[i].ID, i
	}
}

// syncSel keeps the tab, the selection and the scroll valid after any change.
func (m *Model) syncSel() {
	if m.tab >= m.tabsShown() {
		m.tab = 0
	}
	i := m.cursor()
	if i < 0 {
		m.top[m.tab] = 0
		return
	}
	m.sel[m.tab], m.selIx[m.tab] = m.rows()[i].ID, i
	vis := m.layout().visibleRows()
	top := min(m.top[m.tab], i)
	if i >= top+vis {
		top = i - vis + 1
	}
	m.top[m.tab] = min(max(top, 0), max(len(m.rows())-vis, 0))
}

// setSnapshot replaces the list; the daemon owns the order.
func (m *Model) setSnapshot(s ipc.SnapshotResult) {
	m.jobs, m.summary, m.seq = s.Jobs, s.Summary, s.Seq
}

// addSpeed appends one sparkline sample, keeping ui.sparkline_seconds of them.
func (m *Model) addSpeed(v int64) {
	m.speeds = append(m.speeds, v)
	if n := len(m.speeds) - m.o.Settings.UI.SparklineSeconds; n > 0 {
		m.speeds = slices.Clone(m.speeds[n:])
	}
}

// patch applies a progress frame to the rows it names.
func (m *Model) patch(f daemon.Frame) {
	m.summary = f.Summary
	for _, it := range f.Items {
		i := slices.IndexFunc(m.jobs, func(j *store.Job) bool { return j.ID == it.ID })
		if i < 0 {
			continue
		}
		j := *m.jobs[i]
		j.DoneBytes, j.SpeedDown, j.SpeedUp, j.ETASeconds = &it.Done, it.Down, it.Up, nil
		if it.Total > 0 {
			j.TotalBytes = &it.Total
		}
		if it.ETA != nil {
			s := int64(*it.ETA / time.Second)
			j.ETASeconds = &s
		}
		m.jobs[i] = &j
	}
}

// note is the line printed after quitting while downloads go on.
func (m Model) note() string {
	switch n := m.summary.Continuing; n {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf(fmtNoteOne, m.o.Prog)
	default:
		return fmt.Sprintf(fmtNoteMany, n, m.o.Prog)
	}
}
