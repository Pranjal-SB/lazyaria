package tui

import (
	"context"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
)

func (m Model) key(k tea.KeyPressMsg) (Model, tea.Cmd) {
	a := actionFor(k.String())
	switch {
	case k.String() == "ctrl+c":
		return m, tea.Quit
	case m.remove.open():
		return m.removeKey(k)
	case m.add.open:
		return m.formKey(k)
	case m.help:
		if a == actHelp || a == actQuit || k.String() == "esc" {
			m.help = false
		}
		return m, nil
	}
	return m.run(a)
}

// run does what an action means on the list. Keys and footer clicks both
// come here.
func (m Model) run(a string) (Model, tea.Cmd) {
	l := m.layout()
	if slices.Contains(selectionKeys, a) {
		m.selMoved = time.Time{} // the person moved; what they see is what they get
	}
	switch a {
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
		if m.selJustMoved() {
			return m, nil
		}
		return m, m.toggle()
	case actRemove:
		if j := m.selected(); j != nil && slices.Contains(j.Actions, daemon.ActRemove) && !m.selJustMoved() {
			m.remove = newRemoveModal(m.theme, j.ID, j.Name)
		}
	case actUndo:
		return m.undo()
	case actHelp:
		m.help = true
	case actAdd:
		return m.openAdd(nil)
	}
	return m, nil
}

// openAdd opens the add form with links filled in, saving to the daemon's
// folder, which it only knows once connected.
func (m Model) openAdd(links []string) (Model, tea.Cmd) {
	if m.d == nil {
		return m, m.showToast(toast{text: textNotConnected})
	}
	m.add = newAddForm(m.theme, m.status.DownloadDir, links)
	m.add.setWidth(m.formFieldWidth())
	return m, nil
}

func (m Model) formFieldWidth() int { return min(formMaxW, m.layout().w-2) - 7 }

// paste goes to the focused text field. Elsewhere the links in it open
// the add form, unless ui.paste_detection is off.
func (m Model) paste(msg tea.PasteMsg) (Model, tea.Cmd) {
	switch {
	case m.add.open:
		return m.formInput(msg)
	case m.remove.open() || m.help || !m.o.Settings.UI.PasteDetection:
		return m, nil
	}
	links := textutil.ExtractLinks(msg.Content)
	if len(links) == 0 {
		return m, m.showToast(toast{text: textNoLinks})
	}
	return m.openAdd(links)
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

// removeJobs asks the daemon to remove jobs; the reply carries the undo token.
func (m Model) removeJobs(ids []string, name string, deleteFiles bool) tea.Cmd {
	if m.d == nil {
		return nil
	}
	d, ctx := m.d, m.o.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	// No deadline: moving files to the Trash across disks can take long,
	// and giving up here would lose the undo token while the daemon goes on.
	return m.cmd(func() tea.Msg {
		token, _, err := d.Remove(ctx, ids, deleteFiles)
		return doneMsg{act: daemon.ActRemove, ids: ids, name: name, undo: token, err: err}
	})
}

// undo puts back what the toast says was removed, while the toast shows.
func (m Model) undo() (Model, tea.Cmd) {
	token, name, d := m.toast.undo, m.toast.name, m.d
	if token == "" || d == nil {
		return m, nil
	}
	m.toast = toast{}
	return m, m.cmd(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		return doneMsg{act: actUndo, name: name, err: d.Undo(ctx, token)}
	})
}

// done shows how an action went: the daemon's message when it failed.
func (m Model) done(msg doneMsg) (Model, tea.Cmd) {
	switch {
	case msg.err != nil:
		return m, m.showToast(toast{text: msg.err.Error(), bad: true})
	case msg.act == daemon.ActRemove:
		return m, m.showToast(toast{text: fmt.Sprintf(fmtRemoved, msg.name), undo: msg.undo, name: msg.name})
	case msg.act == actUndo:
		return m, m.showToast(toast{text: fmt.Sprintf(fmtRestored, msg.name)})
	}
	return m, nil
}

// Actions that pick what is selected.
var selectionKeys = []string{actDown, actUp, actTop, actBottom, actPageUp, actPageDown,
	actTab1, actTab2, actTab3, actTab4, actNextTab, actPrevTab}

// selectionGuard is how long space and remove do nothing after the
// daemon moved the selection to another job, so a key meant for the job
// that just left does not hit its neighbour.
const selectionGuard = 300 * time.Millisecond

func (m Model) selJustMoved() bool {
	return !m.selMoved.IsZero() && m.now().Sub(m.selMoved) < selectionGuard
}
