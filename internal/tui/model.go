// Package tui is the terminal interface: a thin client of the daemon. It
// formats and lays out what the daemon sends and never derives download
// facts itself. Every daemon call runs in a tea.Cmd.
package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Model is the whole TUI state.
type Model struct {
	o Options

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
	cursor  int
	flash   string // the last action's error
}

func New(o Options) Model { return Model{o: o} }

func (m Model) Init() tea.Cmd { return m.connect() }

func (m Model) cmd(c tea.Cmd) tea.Cmd { return guard(c, m.o.CrashDir) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer logPanic(m.o.CrashDir)
	switch msg := msg.(type) {
	case connectedMsg:
		if msg.gen != m.gen {
			return m, m.closeCmd(msg.d)
		}
		m.d, m.ev, m.fr, m.status = msg.d, msg.ev, msg.fr, msg.status
		m.everUp, m.failures, m.lastErr, m.backoff = true, 0, nil, 0
		m.setSnapshot(msg.snap)
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
		return m, nil
	case frameMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.patch(msg.f)
		return m, m.waitFrame()
	case doneMsg:
		m.flash = ""
		if msg.err != nil {
			m.flash = msg.err.Error()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, max(len(m.jobs)-1, 0))
	case "space":
		j := m.selected()
		if j == nil || m.d == nil {
			return m, nil
		}
		ids := []string{j.ID}
		switch {
		case slices.Contains(j.Actions, daemon.ActPause):
			return m, m.act(daemon.ActPause, ids, func(ctx context.Context, d Daemon) error { return d.Pause(ctx, ids...) })
		case slices.Contains(j.Actions, daemon.ActResume):
			return m, m.act(daemon.ActResume, ids, func(ctx context.Context, d Daemon) error { return d.Resume(ctx, ids...) })
		}
	}
	return m, nil
}

func (m Model) selected() *store.Job {
	if m.cursor < len(m.jobs) {
		return m.jobs[m.cursor]
	}
	return nil
}

// setSnapshot replaces the list; the daemon owns the order.
func (m *Model) setSnapshot(s ipc.SnapshotResult) {
	m.jobs, m.summary, m.seq = s.Jobs, s.Summary, s.Seq
	m.cursor = min(m.cursor, max(len(m.jobs)-1, 0))
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

// View is a skeleton: the top line, one row per job, the footer.
func (m Model) View() tea.View {
	defer logPanic(m.o.CrashDir)
	var b strings.Builder
	switch {
	case m.d != nil:
		b.WriteString(iconConnected)
	case m.everUp:
		b.WriteString(iconReconnecting + " " + textReconnecting)
	default:
		b.WriteString(iconReconnecting + " " + textConnecting)
	}
	fmt.Fprintf(&b, "  "+fmtSummary+"\n\n", m.summary.Active, m.summary.Waiting, m.summary.Done)
	if len(m.jobs) == 0 {
		b.WriteString(textNoJobs + "\n")
	}
	for i, j := range m.jobs {
		mark := "  "
		if i == m.cursor {
			mark = "▌ "
		}
		fmt.Fprintf(&b, "%s#%d %s  %s\n", mark, j.Num, j.Name, j.Label)
	}
	b.WriteString("\n")
	switch {
	case m.failures >= failuresShown && m.lastErr != nil:
		msg := m.lastErr.Error()
		if !strings.Contains(msg, "doctor") {
			msg = fmt.Sprintf(fmtDoctor, m.lastErr, m.o.Prog)
		}
		b.WriteString(msg)
	case m.flash != "":
		b.WriteString(m.flash)
	default:
		b.WriteString(textKeys)
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}
