//go:build e2ecrash

package daemon

import (
	"os"
	"syscall"
)

// Built only into the end-to-end tests' binary (-tags e2ecrash):
// LAZYARIA_E2E_CRASH_AT=<step> kills the daemon at that publish step, as a
// power cut would, so the tests can check the next start finishes the job.
func init() {
	at := os.Getenv("LAZYARIA_E2E_CRASH_AT")
	if at == "" {
		return
	}
	publishStop = func(step string) bool {
		if step == at {
			syscall.Kill(os.Getpid(), syscall.SIGKILL)
			select {} // the signal ends the process
		}
		return false
	}
}
