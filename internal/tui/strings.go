package tui

// Every user-facing string of the TUI lives here, so a translation
// catalogue can replace them in one place.
const (
	textApp          = "lazyaria"
	textConnecting   = "connecting"
	textReconnecting = "reconnecting"
	textEngine       = "aria2"
	textEngineOK     = "ok"
	fmtDoctor        = "%v; run `%s doctor` to see what is wrong"
	fmtNoteOne       = "1 download continues in the background; run %s to come back."
	fmtNoteMany      = "%d downloads continue in the background; run %s to come back."
	fmtSpeed         = "%s/s"
	fmtFinishAll     = "about %s to finish all"
	fmtLeft          = "%s left"
	fmtOf            = "%s of %s"
	fmtPercent       = "%d%%"
	fmtFinishedAgo   = "finished %s"
	fmtAgo           = "%s ago"
	textJustNow      = "just now"
	fmtNum           = "#%d"
	fmtChipFailed    = "%d failed, press 4 to see"
	textTooSmall     = "Make the terminal larger to see details"

	tabDownloading = "Downloading"
	tabWaiting     = "Waiting"
	tabFinished    = "Finished"
	tabProblems    = "Problems"
	titleDetails   = "Details"

	emptyDownloading = "Nothing downloading."
	emptyWaiting     = "Nothing waiting."
	emptyFinished    = "Finished downloads show up here."
	emptyProblems    = "No problems."
	emptyAddKey      = "a"
	fmtEmptyAdd      = "Paste a link or press %s to add one."
	emptyBackKey     = "1"
	fmtEmptyBack     = "Press %s to see what is downloading."
	emptyKeepGoing   = "Downloads keep going after you quit."

	hintAdd     = "add"
	hintPause   = "pause"
	hintResume  = "resume"
	hintRemove  = "remove"
	hintDetails = "details"
	hintHelp    = "keys"
	hintQuit    = "quit"
)
