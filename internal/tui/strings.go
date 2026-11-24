package tui

// Every user-facing string of the TUI lives here, so a translation
// catalogue can replace them in one place (spec §7.7).
const (
	iconConnected    = "●"
	iconReconnecting = "◐"
	textConnecting   = "connecting"
	textReconnecting = "reconnecting"
	textNoJobs       = "No downloads yet."
	textKeys         = "space pause/resume · ↑↓ move · q quit"
	fmtDoctor        = "%v; run `%s doctor` to see what is wrong"
	fmtSummary       = "%d downloading · %d waiting · %d finished"
	fmtNoteOne       = "1 download continues in the background; run %s to come back."
	fmtNoteMany      = "%d downloads continue in the background; run %s to come back."
)
