//go:build e2e && linux

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// procs returns every live process started for this env: the daemon, its
// aria2c and anything else that inherited the runtime dir.
func (e *env) procs() []int {
	var pids []int
	ds, _ := os.ReadDir("/proc")
	for _, d := range ds {
		pid, err := strconv.Atoi(d.Name())
		if err != nil || !alive(pid) {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		if err == nil && bytes.Contains(b, []byte("\x00XDG_RUNTIME_DIR="+e.rt+"\x00")) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// cleanup shows the daemon log of a failed test and stops what is left.
func (e *env) cleanup() {
	if e.t.Failed() {
		b, _ := os.ReadFile(e.p.Log)
		e.t.Logf("daemon log:\n%s", b)
	}
	for _, pid := range e.procs() {
		syscall.Kill(pid, syscall.SIGKILL)
	}
	// Let them die before TempDir removal runs, or files appear mid-delete.
	for end := time.Now().Add(3 * time.Second); len(e.procs()) > 0 && time.Now().Before(end); {
		time.Sleep(50 * time.Millisecond)
	}
}

func alive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// A zombie has exited; only its parent has not reaped it yet.
	f := strings.Fields(string(b[bytes.LastIndexByte(b, ')')+1:]))
	return len(f) > 0 && f[0] != "Z"
}

// aria2Child returns the pid of the daemon's aria2c child, or 0.
func aria2Child(daemonPID int) int {
	ds, _ := os.ReadDir("/proc")
	for _, d := range ds {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		s := string(b)
		comm := s[strings.IndexByte(s, '(')+1 : strings.LastIndexByte(s, ')')]
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if comm == "aria2c" && len(f) > 1 && f[0] != "Z" && f[1] == strconv.Itoa(daemonPID) {
			return pid
		}
	}
	return 0
}
