// Package form holds the small input widgets the TUI's dialogs are made
// of: a text field, a checkbox and a row of buttons.
package form

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Field is a one-line text input. Its cursor does not blink, so the
// screen only changes when something happens.
type Field struct {
	Label    string
	Disabled bool
	in       textinput.Model
}

// NewField makes a field holding value; placeholder shows, in muted, while it is empty.
func NewField(label, value, placeholder string, muted lipgloss.Style) Field {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	plain := lipgloss.NewStyle()
	in.SetStyles(textinput.Styles{
		Focused: textinput.StyleState{Text: plain, Placeholder: muted, Suggestion: muted, Prompt: plain},
		Blurred: textinput.StyleState{Text: plain, Placeholder: muted, Suggestion: muted, Prompt: plain},
		Cursor:  textinput.CursorStyle{Shape: tea.CursorBlock},
	})
	in.KeyMap.Paste.SetEnabled(false) // the terminal's own paste arrives as a paste message
	in.SetValue(value)
	return Field{Label: label, in: in}
}

func (f *Field) Focus()            { f.in.Focus() }
func (f *Field) Blur()             { f.in.Blur() }
func (f Field) Focused() bool      { return f.in.Focused() }
func (f Field) Value() string      { return f.in.Value() }
func (f *Field) SetValue(s string) { f.in.SetValue(s) }

// SetWidth sets how many cells the text shows; longer text scrolls.
func (f *Field) SetWidth(w int) {
	f.in.SetWidth(w)
	f.in.SetValue(f.in.Value()) // scrolls the text to fit the new width
}

// Update edits the field with keys and pastes while it has focus.
func (f Field) Update(msg tea.Msg) (Field, tea.Cmd) {
	var cmd tea.Cmd
	f.in, cmd = f.in.Update(msg)
	return f, cmd
}

// View is the input line, without the label.
func (f Field) View() string { return f.in.View() }
