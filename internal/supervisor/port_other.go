//go:build !linux

package supervisor

// portState needs the NOTICE "listening" line from aria2's log.
const logLevel = "notice"

// portState has no socket table to ask here, so it trusts the port only
// after this run's log says aria2 bound it, and never after a failed bind.
// waitReady checks again after getVersion succeeds.
func (sv *Supervisor) portState(pr *proc) (portState, error) {
	return logPortState(readFrom(sv.logPath(), pr.logFrom), pr.port), nil
}
