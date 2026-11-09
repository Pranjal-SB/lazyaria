package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
)

// Endpoint is a running aria2c: enough for a re-exec'd daemon to take it over.
type Endpoint struct {
	PID    int    `json:"pid"`
	Port   int    `json:"port"`
	Secret string `json:"secret"`
}

// Endpoint returns the aria2c that is running now, if any.
func (sv *Supervisor) Endpoint() (Endpoint, bool) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.cur == nil {
		return Endpoint{}, false
	}
	select {
	case <-sv.cur.exited:
		return Endpoint{}, false
	default:
	}
	return Endpoint{PID: sv.cur.cmd.Process.Pid, Port: sv.cur.port, Secret: sv.cur.secret}, true
}

// Freeze ends supervision without stopping aria2c, so it can be handed to
// a re-exec'd daemon, and returns it if one is running. Nothing respawns
// after this; Shutdown still stops a frozen aria2c.
func (sv *Supervisor) Freeze() (Endpoint, bool) {
	sv.frozen.Store(true)
	sv.cancel()
	sv.mu.Lock()
	started := sv.started
	sv.mu.Unlock()
	if started {
		<-sv.done
	}
	return sv.Endpoint()
}

// Adopt makes Start take over ep, an aria2c this process started before
// it re-exec'd itself, instead of launching a new one. If ep does not
// answer, Start kills it and launches as usual. Call it before Start.
func (sv *Supervisor) Adopt(ep Endpoint) {
	sv.mu.Lock()
	sv.adopt = &ep
	sv.mu.Unlock()
}

// takeOver waits on the adopted child (still ours: exec keeps the pid)
// and checks that it answers on its port.
func (sv *Supervisor) takeOver(ep Endpoint) (*proc, *aria2.Client, error) {
	p, err := os.FindProcess(ep.PID)
	if err != nil {
		return nil, nil, err
	}
	pr := &proc{cmd: &exec.Cmd{Process: p}, port: ep.Port, secret: ep.Secret, started: time.Now(), exited: make(chan struct{})}
	sv.mu.Lock()
	sv.cur = pr
	sv.mu.Unlock()
	go func() {
		st, err := p.Wait()
		if err == nil && !st.Success() {
			err = &exec.ExitError{ProcessState: st}
		}
		pr.err, pr.exitedAt = err, time.Now()
		close(pr.exited)
	}()
	// The port was checked when this aria2c was launched and it has held
	// it since, so only its answer matters now.
	url := fmt.Sprintf("ws://127.0.0.1:%d/jsonrpc", ep.Port)
	for end := time.Now().Add(sv.readyTimeout); time.Now().Before(end); {
		if c := sv.tryReady(url, ep.Secret); c != nil {
			return pr, c, nil
		}
		if pr.waitFor(readyPoll) {
			break
		}
	}
	select {
	case <-pr.exited: // reaped, or not our child: its pid may belong to someone else now
	default:
		p.Kill()
		<-pr.exited
	}
	return nil, nil, fmt.Errorf("the aria2c handed over by the previous daemon (pid %d) did not answer", ep.PID)
}
