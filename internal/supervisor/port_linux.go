//go:build linux

package supervisor

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The socket table tells us who owns the port, so aria2 can log quietly.
const logLevel = "warn"

// portState asks the kernel who listens on 127.0.0.1:port: the socket inode
// from /proc/net/tcp must be one of the child's open fds.
func (sv *Supervisor) portState(pr *proc) (portState, error) {
	// A squatter on 0.0.0.0 or [::] blocks the bind without showing up on
	// 127.0.0.1 in the table, so aria2's own report counts too.
	if sv.bindFailed(pr) {
		return portStolen, nil
	}
	inodes, err := listenInodes(pr.port)
	if err != nil || len(inodes) == 0 {
		return portPending, err
	}
	dir := fmt.Sprintf("/proc/%d/fd", pr.cmd.Process.Pid)
	fds, err := os.ReadDir(dir)
	if err != nil {
		return portPending, nil // the child is gone; waitReady sees the exit
	}
	for _, fd := range fds {
		link, _ := os.Readlink(filepath.Join(dir, fd.Name()))
		if inode, ok := strings.CutPrefix(link, "socket:["); ok && inodes[strings.TrimSuffix(inode, "]")] {
			return portOurs, nil
		}
	}
	return portStolen, nil
}

// listenInodes returns the inodes of LISTEN sockets on 127.0.0.1:port.
func listenInodes(port int) (map[string]bool, error) {
	b, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return nil, fmt.Errorf("cannot verify aria2's RPC port: /proc/net/tcp unreadable (%w); check that /proc is mounted or run `lazyaria doctor`", err)
	}
	// The kernel prints the address as a host-order u32 and the port as hex.
	local := fmt.Sprintf("%08X:%04X", binary.NativeEndian.Uint32([]byte{127, 0, 0, 1}), port)
	inodes := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) > 9 && f[1] == local && f[3] == "0A" {
			inodes[f[9]] = true
		}
	}
	return inodes, nil
}
