package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/addlink"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
	"github.com/Pranjal-SB/lazyaria/internal/tui/form"
)

// addItem is one link and the form's choices for it: folder, file name,
// paused, forced.
type addItem struct {
	link string
	p    daemon.AddParams
}

// resolve turns the link into the add, reading a local .torrent file.
func resolve(it addItem) (daemon.AddParams, error) {
	p, err := addlink.Params(it.link)
	p.Dir, p.Out, p.StartPaused, p.Force = it.p.Dir, it.p.Out, it.p.StartPaused, it.p.Force
	return p, err
}

type addFailure struct{ link, msg string }

// addTimeout is how long one add may take; a var so tests can shorten it.
var addTimeout = callTimeout

// addMsg is how far a batch got: it ends, or pauses at a duplicate.
type addMsg struct {
	batch          int
	added, skipped []*store.Job
	failed         []addFailure
	unsure         []string
	dup            *store.Job // the batch paused: this job is already in the list
	dupItem        addItem
	rest           []addItem // not tried yet
}

// submitForm adds the form's links. With none checked there is nothing to add.
func (m Model) submitForm() (Model, tea.Cmd) {
	if m.add.list && m.add.checked() == 0 {
		return m, nil
	}
	return m.submit(m.add.items())
}

// submit adds the links one at a time, each with its own deadline. It
// goes past links the daemon refuses and pauses at a duplicate.
func (m Model) submit(items []addItem) (Model, tea.Cmd) {
	if m.add.busy || !m.add.open {
		return m, nil
	}
	if len(items) == 0 {
		return m.finishAdd()
	}
	if m.d == nil {
		// Nothing goes out: the links not added yet stay in the field.
		for _, it := range items {
			m.add.failed = append(m.add.failed, addFailure{it.link, textNotConnected})
		}
		return m.finishAdd()
	}
	m.addSeq++
	m.add.busy, m.add.msg, m.add.bad, m.add.batch = true, "", false, m.addSeq
	d, si, batch := m.d, m.o.Settings.UI.Units == "si", m.addSeq
	return m, m.cmd(func() tea.Msg {
		r := addMsg{batch: batch}
		for i, it := range items {
			p, err := resolve(it)
			if err != nil {
				r.failed = append(r.failed, addFailure{it.link, err.Error()})
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), addTimeout)
			j, err := d.Add(ctx, p)
			late := ctx.Err() != nil
			cancel()
			dup, isDup := errors.AsType[*daemon.DuplicateError](err)
			skip, isSkip := errors.AsType[*daemon.SkippedError](err)
			switch {
			case err == nil:
				r.added = append(r.added, j)
			case late:
				// The daemon may still add it after we stop waiting, so
				// trying again could add it twice.
				r.unsure = append(r.unsure, it.link)
			case isSkip:
				if skip.Existing != nil {
					r.skipped = append(r.skipped, skip.Existing)
				}
			case isDup && dup.Existing != nil:
				r.dup, r.dupItem, r.rest = dup.Existing, it, items[i+1:]
				return r
			default:
				r.failed = append(r.failed, addFailure{it.link, addErrText(err, si)})
			}
		}
		return r
	})
}

// addErrText is the daemon's refusal in plain words.
func addErrText(err error, si bool) string {
	if dup, ok := errors.AsType[*daemon.DuplicateError](err); ok {
		return fmt.Sprintf(fmtFileThere, home(dup.Path))
	}
	if ns, ok := errors.AsType[*daemon.NoSpaceError](err); ok {
		return fmt.Sprintf(fmtNoSpace, home(ns.Dir), textutil.Bytes(int64(ns.Need), si), textutil.Bytes(int64(ns.Free), si))
	}
	return err.Error()
}

// added takes in how far the batch got, then asks about a duplicate or
// ends it. A batch from a form that was closed or has started another
// only reports what it added; its question is dropped with it.
func (m Model) added(msg addMsg) (Model, tea.Cmd) {
	if !m.add.open || msg.batch != m.add.batch {
		if note := addNote(msg.added, msg.skipped, msg.unsure); note != "" {
			return m, m.showToast(toast{text: note})
		}
		return m, nil
	}
	f := &m.add
	f.busy = false
	f.added = append(f.added, msg.added...)
	f.skipped = append(f.skipped, msg.skipped...)
	f.failed = append(f.failed, msg.failed...)
	f.unsure = append(f.unsure, msg.unsure...)
	if msg.dup != nil {
		f.dup, f.dupItem, f.rest = msg.dup, msg.dupItem, msg.rest
		label := f.dup.Label
		if label == "" {
			label = string(f.dup.State)
		}
		f.msg, f.bad = fmt.Sprintf(fmtDuplicate, f.dup.Num, label), false
		f.dupB = form.Buttons{Labels: []string{btnAgain, btnShow, btnCancel}}
		return m, nil
	}
	return m.finishAdd()
}

// dupPress answers the duplicate question, then the batch goes on.
func (m Model) dupPress(i int) (Model, tea.Cmd) {
	f := &m.add
	if f.dup == nil {
		return m, nil
	}
	items := f.rest
	switch i {
	case dupAgain:
		again := f.dupItem
		again.p.Force = true
		items = append([]addItem{again}, items...)
	case dupShow:
		if id := f.dup.ID; slices.ContainsFunc(m.jobs, func(j *store.Job) bool { return j.ID == id }) {
			m.selWant, f.chosen = id, true
		}
	}
	f.dup, f.dupItem, f.rest, f.msg = nil, addItem{}, nil, ""
	return m.submit(items)
}

// addNote says what a batch did, for a toast.
func addNote(added, skipped []*store.Job, unsure []string) string {
	var note []string
	switch n := len(added); {
	case n == 1 && added[0] != nil:
		note = append(note, fmt.Sprintf(fmtAdded, added[0].Num, added[0].Name))
	case n > 0:
		note = append(note, fmt.Sprintf(fmtAddedMany, n))
	}
	switch n := len(skipped); {
	case n == 1 && len(note) == 0:
		note = append(note, fmt.Sprintf(fmtDuplicate, skipped[0].Num, string(skipped[0].State)))
	case n > 0:
		note = append(note, fmt.Sprintf(fmtSkippedMany, n))
	}
	switch n := len(unsure); {
	case n == 1:
		note = append(note, fmt.Sprintf(fmtUnsureOne, unsure[0]))
	case n > 1:
		note = append(note, fmt.Sprintf(fmtUnsureMany, n))
	}
	return strings.Join(note, "; ")
}

// finishAdd ends a batch. With every link added the form closes; links
// that failed stay in the field, alone, with the first reason. Links that
// may have been added are not kept, so trying again cannot add them twice.
func (m Model) finishAdd() (Model, tea.Cmd) {
	f := m.add
	note := addNote(f.added, f.skipped, f.unsure)
	if n := len(f.added); n > 0 && !f.chosen && f.added[n-1] != nil {
		m.selWant = f.added[n-1].ID
	}
	if len(f.failed) == 0 {
		m.add = addForm{}
	} else {
		var links []string
		for _, x := range f.failed {
			links = append(links, x.link)
		}
		m.add.setList(links)
		m.add.msg, m.add.bad = f.failed[0].msg, true
		if len(f.failed) > 1 {
			m.add.msg = fmt.Sprintf(fmtFailedMany, len(f.failed), f.failed[0].msg)
		}
		m.add.added, m.add.skipped, m.add.failed, m.add.unsure, m.add.chosen = nil, nil, nil, nil, false
		m.add.setFocus(stopLink)
	}
	if note == "" {
		return m, nil
	}
	return m, m.showToast(toast{text: note})
}
