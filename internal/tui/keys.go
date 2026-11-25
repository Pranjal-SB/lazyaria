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
	actRemove   = "remove"
	actUndo     = "undo"
	actHelp     = "help"
	actAdd      = "add"
	actDetDown  = "details_down"
	actDetUp    = "details_up"
	actQuit     = "quit"
)

// keyDef binds keys to an action. The table is the one place keys live:
// the key handler, the footer and the help read it, so rebinding keys
// later only has to edit it.
type keyDef struct {
	action string
	keys   []string // as tea.KeyPressMsg.String() names them
	help   string
}

var keyTable = []keyDef{
	{actDown, []string{"j", "down"}, helpDown},
	{actUp, []string{"k", "up"}, helpUp},
	{actTop, []string{"g", "home"}, helpTop},
	{actBottom, []string{"G", "end"}, helpBottom},
	{actPageUp, []string{"pgup", "ctrl+u"}, helpPageUp},
	{actPageDown, []string{"pgdown", "ctrl+d"}, helpPageDown},
	{actTab1, []string{"1"}, helpTab1},
	{actTab2, []string{"2"}, helpTab2},
	{actTab3, []string{"3"}, helpTab3},
	{actTab4, []string{"4"}, helpTab4},
	{actNextTab, []string{"]", "tab"}, helpNextTab},
	{actPrevTab, []string{"[", "shift+tab"}, helpPrevTab},
	{actDetails, []string{"enter"}, helpDetails},
	{actDetDown, []string{"J"}, helpDetDown},
	{actDetUp, []string{"K"}, helpDetUp},
	{actAdd, []string{"a"}, helpAdd},
	{actToggle, []string{"space"}, helpToggle},
	{actRemove, []string{"x", "delete"}, helpRemove},
	{actUndo, []string{"u"}, helpUndo},
	{actHelp, []string{"?"}, helpHelp},
	{actQuit, []string{"q", "ctrl+c"}, helpQuit},
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

// KeyDoc is one line of the key reference: the keys, the action they run
// and what it does.
type KeyDoc struct {
	Keys         []string
	Action, Help string
}

// KeyHelp is the key table, for the generated key reference.
func KeyHelp() []KeyDoc {
	out := make([]KeyDoc, len(keyTable))
	for i, d := range keyTable {
		out[i] = KeyDoc{Keys: slices.Clone(d.keys), Action: d.action, Help: d.help}
	}
	return out
}
