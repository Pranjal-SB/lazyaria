//go:build linux

package supervisor

import (
	"bytes"
	"os"
	"slices"
	"strconv"
	"syscall"
	"time"
)

// stopStale stops any aria2c that an earlier daemon of this instance
// launched and that outlived it: after a SIGKILL of the daemon, aria2c
// notices only at its next --stop-with-process check, about a second later,
// and until it exits it still writes into the staging folders the new
// engine takes over. It is found by our conf path; one whose
// --stop-with-process is this process is ours (also across an upgrade
// re-exec, which keeps the pid) and is left alone.
func (sv *Supervisor) stopStale() {
	conf := []byte("--conf-path=" + sv.p.AriaConf)
	own := []byte("--stop-with-process=" + strconv.Itoa(os.Getpid()))
	ds, _ := os.ReadDir("/proc")
	for _, d := range ds {
		pid, err := strconv.Atoi(d.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		b, err := os.ReadFile("/proc/" + d.Name() + "/cmdline")
		if err != nil {
			continue
		}
		args := bytes.Split(b, []byte{0})
		if !slices.ContainsFunc(args, func(a []byte) bool { return bytes.Equal(a, conf) }) ||
			slices.ContainsFunc(args, func(a []byte) bool { return bytes.Equal(a, own) }) {
			continue
		}
		sv.log.Warn("stopping the aria2c a previous daemon left running", "pid", pid)
		syscall.Kill(pid, syscall.SIGTERM) // a graceful stop saves its control files
		if !waitExit(pid, 10*time.Second) {
			syscall.Kill(pid, syscall.SIGKILL)
			waitExit(pid, 5*time.Second)
		}
	}
}

func waitExit(pid int, d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if gone(pid) {
			return true
		}
	}
	return gone(pid)
}

// gone: pid has exited (a zombie has).
func gone(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	i := bytes.LastIndexByte(b, ')')
	return i < 0 || i+2 >= len(b) || b[i+2] == 'Z'
}
