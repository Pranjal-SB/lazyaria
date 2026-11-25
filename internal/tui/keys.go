package tui

import "slices"

// Actions a key can run.
const (
	actDown     = "down"
	actUp       = "up"
	actTop      = "top"
	actBottom   = "bottom"
	actPageUp   = "page_up"
	actPageDown = "page_down"
	actTab1     = "tab_1"
	actTab2     = "tab_2"
	actTab3     = "tab_3"
	actTab4     = "tab_4"
	actNextTab  = "next_tab"
	actPrevTab  = "prev_tab"
	actDetails  = "details"
	actToggle   = "pause_resume"
	actQuit     = "quit"
)

// keyDef binds keys to an action. The table is the one place keys live:
// the key handler and the help read it, so rebinding keys later only
// has to edit it.
type keyDef struct {
	action string
	keys   []string // as tea.KeyPressMsg.String() names them
}

var keyTable = []keyDef{
	{actDown, []string{"j", "down"}},
	{actUp, []string{"k", "up"}},
	{actTop, []string{"g", "home"}},
	{actBottom, []string{"G", "end"}},
	{actPageUp, []string{"pgup", "ctrl+u"}},
	{actPageDown, []string{"pgdown", "ctrl+d"}},
	{actTab1, []string{"1"}},
	{actTab2, []string{"2"}},
	{actTab3, []string{"3"}},
	{actTab4, []string{"4"}},
	{actNextTab, []string{"]", "tab"}},
	{actPrevTab, []string{"[", "shift+tab"}},
	{actDetails, []string{"enter"}},
	{actToggle, []string{"space"}},
	{actQuit, []string{"q", "ctrl+c"}},
}

// actionFor is the action bound to key, or "".
func actionFor(key string) string {
	for _, d := range keyTable {
		if slices.Contains(d.keys, key) {
			return d.action
		}
	}
	return ""
}

// keyFor is the first key bound to action, as the footer shows it.
func keyFor(action string) string {
	for _, d := range keyTable {
		if d.action == action {
			return d.keys[0]
		}
	}
	return ""
}
