package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/ipc"
	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
)

// handoffEnv carries the inherited fds and the running aria2c's port and
// secret to the re-exec'd image (spec §19.1). The new image unsets it at
// once, so no child ever sees it, and it is never on argv.
const handoffEnv = "LAZYARIA_UPGRADE"

type handoff struct {
	ListenFD int                  `json:"listen_fd"`
	LockFD   int                  `json:"lock_fd"`
	Aria2    *supervisor.Endpoint `json:"aria2,omitempty"`
}

func takeHandoff() (*handoff, error) {
	v, ok := os.LookupEnv(handoffEnv)
	os.Unsetenv(handoffEnv)
	if !ok {
		return nil, nil
	}
	var h handoff
	if err := json.Unmarshal([]byte(v), &h); err != nil {
		return nil, fmt.Errorf("unreadable %s from the previous daemon: %w; run `lazyaria doctor`", handoffEnv, err)
	}
	return &h, nil
}

// upgradeState is everything the exec needs once serving has stopped.
type upgradeState struct {
	listenFD, lockFD int
	env              []string
}

// prepareUpgrade readies the socket and lock for the exec and freezes
// aria2c's supervision, so no respawn can start a second, untracked
// aria2c. It must run before Serve stops, which closes the listener. A nil
// result means the exec cannot happen and the daemon just shuts down.
func (d *daemonProc) prepareUpgrade() *upgradeState {
	// The daemon.upgrade reply goes out after the handler returns; give it
	// time to reach the client before the connection is cut.
	time.Sleep(200 * time.Millisecond)
	listenFD, lockFD, err := ipc.Handoff(d.l, d.lock)
	if err != nil {
		d.log.Error("cannot upgrade in place; shutting down instead", "err", err)
		return nil
	}
	h := handoff{ListenFD: listenFD, LockFD: lockFD}
	if d.sv != nil {
		if ep, ok := d.sv.Freeze(); ok {
			h.Aria2 = &ep
		}
	}
	b, _ := json.Marshal(h)
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, handoffEnv+"=") })
	d.log.Info("upgrading in place", "pid", os.Getpid())
	return &upgradeState{listenFD: listenFD, lockFD: lockFD, env: append(env, handoffEnv+"="+string(b))}
}

// exec replaces this process with the current binary. The pid stays, so
// aria2c's --stop-with-process still points at us. It returns only on failure.
func (u *upgradeState) exec() error {
	defer syscall.Close(u.listenFD)
	exe, err := os.Executable()
	if err == nil {
		err = ipc.KeepOnExec(u.listenFD, u.lockFD)
	}
	if err == nil {
		err = syscall.Exec(exe, os.Args, u.env)
	}
	return fmt.Errorf("exec %s: %w", exe, err)
}
