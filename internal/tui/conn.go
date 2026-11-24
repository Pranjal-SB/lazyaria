package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/ipc"
)

const (
	connectTimeout = 15 * time.Second // covers starting the daemon
	callTimeout    = 10 * time.Second
	debounce       = 100 * time.Millisecond
	backoffMin     = 250 * time.Millisecond
	backoffMax     = 5 * time.Second
	failuresShown  = 3 // failed connects before the footer shows the error
)

// Messages carry the connection generation they belong to; a message from
// a connection already given up is dropped.
type (
	connectedMsg struct {
		gen    int
		d      Daemon
		snap   ipc.SnapshotResult
		status ipc.Status
		ev     <-chan daemon.Event
		fr     <-chan daemon.Frame
	}
	disconnectedMsg struct {
		gen int
		err error
	}
	retryMsg    struct{ gen int }
	debounceMsg struct{ gen int }
	eventMsg    struct {
		gen int
		ev  daemon.Event
	}
	frameMsg struct {
		gen int
		f   daemon.Frame
	}
	snapshotMsg struct {
		gen  int
		snap ipc.SnapshotResult
		err  error
	}
	doneMsg struct {
		act string
		ids []string
		err error
	}
)

// connect dials, subscribes, then takes the snapshot and the status, in
// that order, so no change between them is missed.
func (m Model) connect() tea.Cmd {
	gen, o := m.gen, m.o
	return m.cmd(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()
		d, err := o.Dial(ctx)
		if err != nil {
			return disconnectedMsg{gen, err}
		}
		msg := connectedMsg{gen: gen, d: d}
		msg.ev, msg.fr, err = d.Subscribe(ctx, true)
		if err == nil {
			msg.snap, err = d.List(ctx, true, o.Settings.History.ListLimit)
		}
		if err == nil {
			msg.status, err = d.Status(ctx)
		}
		if err != nil {
			d.Close()
			return disconnectedMsg{gen, err}
		}
		return msg
	})
}

func (m Model) waitEvent() tea.Cmd {
	gen, ev := m.gen, m.ev
	return m.cmd(func() tea.Msg {
		e, ok := <-ev
		if !ok {
			return disconnectedMsg{gen: gen}
		}
		return eventMsg{gen, e}
	})
}

func (m Model) waitFrame() tea.Cmd {
	gen, fr := m.gen, m.fr
	return m.cmd(func() tea.Msg {
		f, ok := <-fr
		if !ok {
			return disconnectedMsg{gen: gen}
		}
		return frameMsg{gen, f}
	})
}

func (m Model) refetch() tea.Cmd {
	gen, d, limit := m.gen, m.d, m.o.Settings.History.ListLimit
	return m.cmd(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		snap, err := d.List(ctx, true, limit)
		return snapshotMsg{gen, snap, err}
	})
}

// act runs a job action on the daemon.
func (m Model) act(name string, ids []string, do func(context.Context, Daemon) error) tea.Cmd {
	d := m.d
	return m.cmd(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		return doneMsg{act: name, ids: ids, err: do(ctx, d)}
	})
}

func (m Model) closeCmd(d Daemon) tea.Cmd {
	return m.cmd(func() tea.Msg { d.Close(); return nil })
}

// nextBackoff doubles the wait between reconnects, from backoffMin up to backoffMax.
func nextBackoff(d time.Duration) time.Duration {
	return min(max(2*d, backoffMin), backoffMax)
}
