//go:build !linux

package supervisor

// portState has no socket table to ask here, so it relies on aria2 logging
// a failed IPv4 RPC bind. waitReady checks again after getVersion succeeds.
func (sv *Supervisor) portState(pr *proc) portState {
	if sv.bindFailed(pr) {
		return portStolen
	}
	return portOurs
}
