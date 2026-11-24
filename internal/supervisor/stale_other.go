//go:build !linux

package supervisor

// stopStale has no process table to read here.
// shortcut: an aria2c a SIGKILLed daemon left may overlap the next one's
// for up to a second; port it to the platform's process API if it matters.
func (sv *Supervisor) stopStale() {}
