// Package tui is the terminal interface: a thin client of the daemon. It
// formats and lays out what the daemon sends and never derives download
// facts itself. Every daemon call runs in a tea.Cmd.
package tui

import (
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
	toast       toast             // the note in the footer
	toastSeq    int
	remove      removeModal // the remove dialog, when open
	add         addForm     // the add form, when open
	selWant     string      // a job to select once the list has it
	selMoved    time.Time   // when the daemon last moved the selection to another job
	help        bool        // the key help is open
	detTop      int         // the first details line shown
	dumb        bool        // TERM=dumb: ASCII glyphs, no links

	det     daemon.Details // the last details reply
	detID   string         // the job the pane shows; "" while it is hidden
	detSeq  int            // bumped when the job changes, so older ticks are dropped
	detAt   time.Time      // when the last fetch started
	detBusy bool           // a fetch is running
	detDue  bool           // a fetch is scheduled
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
	before := m.selectedID()
	m, cmd := m.update(msg)
	m.syncSel()
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseMsg:
	default:
		if after := m.selectedID(); before != "" && after != before {
			m.selMoved = m.now()
		}
	}
	return m, tea.Batch(cmd, m.watchDetails(msg))
}

func (m Model) update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.add.setWidth(m.formFieldWidth())
	case tea.PasteMsg:
		return m.paste(msg)
	case addMsg:
		return m.added(msg)
	case tea.EnvMsg:
		if msg.Getenv("TERM") == "dumb" {
			m.theme, m.dumb = NewTheme("ascii"), true
		}
	case connectedMsg:
		if msg.gen != m.gen {
			return m, m.closeCmd(msg.d)
		}
		m.d, m.ev, m.fr, m.status = msg.d, msg.ev, msg.fr, msg.status
		m.everUp, m.failures, m.lastErr, m.backoff = true, 0, nil, 0
		m.detID = "" // fetch the details again from the new connection
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
		if p, ok := msg.ev.Data["phase"].(string); ok && msg.ev.Type == "engine.state" {
			m.status.EngineState = p
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
	case detailsMsg:
		m.gotDetails(msg)
	case doneMsg:
		return m.done(msg)
	case toastDoneMsg:
		if msg.seq == m.toast.seq {
			m.toast = toast{}
		}
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.MouseMsg:
		return m.mouse(msg)
	}
	return m, nil
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

func (m Model) selectedID() string {
	if j := m.selected(); j != nil {
		return j.ID
	}
	return ""
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
	if i := slices.IndexFunc(m.jobs, func(j *store.Job) bool { return j.ID == m.selWant }); i >= 0 {
		if t := tabOf(m.jobs[i].State); t < m.tabsShown() {
			m.tab, m.sel[t] = t, m.selWant
		}
		m.selWant = ""
	}
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
