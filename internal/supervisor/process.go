package supervisor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
)

const (
	readyPoll      = 50 * time.Millisecond
	readyTimeout   = 3 * time.Second
	maxBindRetries = 5
	tailLines      = 20
)

var errBindRace = errors.New("aria2c could not bind its RPC port")

// proc is one aria2c run. exitedAt and err are set before exited is closed.
type proc struct {
	cmd      *exec.Cmd
	port     int
	secret   string
	logFrom  int64 // aria2.log size at spawn; later bytes belong to this run
	started  time.Time
	exited   chan struct{}
	exitedAt time.Time
	err      error
}

func (pr *proc) waitFor(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-pr.exited:
		return true
	case <-t.C:
		return false
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find a free loopback port: %w (run `lazyaria doctor`)", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func newSecret() string {
	b := make([]byte, 16)
	rand.Read(b) // never fails; the runtime aborts if the OS source is broken
	return hex.EncodeToString(b)
}

func (sv *Supervisor) logPath() string { return filepath.Join(sv.p.StateDir, "aria2.log") }

// launch starts aria2c and waits until it answers. A port lost to another
// process between our probe and aria2's bind is retried with a new port. On
// failure the returned proc, if any, has already been reaped.
func (sv *Supervisor) launch() (*proc, *aria2.Client, error) {
	for try := 0; ; try++ {
		port, err := sv.pickPort()
		if err != nil {
			return nil, nil, err
		}
		pr, err := sv.spawn(port, newSecret())
		if err != nil {
			return nil, nil, err
		}
		c, err := sv.waitReady(pr)
		if err == nil {
			return pr, c, nil
		}
		pr.cmd.Process.Kill()
		<-pr.exited
		if errors.Is(err, errBindRace) && try < maxBindRetries {
			sv.log.Info("aria2c lost its RPC port to another process; retrying on a new port", "port", port)
			continue
		}
		return pr, nil, err
	}
}

func (sv *Supervisor) spawn(port int, secret string) (*proc, error) {
	if err := WriteConf(sv.p.AriaConf, sv.s, sv.p, port, secret); err != nil {
		return nil, err
	}
	logf, err := os.OpenFile(sv.logPath(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open aria2 log: %w (check the state dir or run `lazyaria doctor`)", err)
	}
	defer logf.Close()
	fi, err := logf.Stat()
	if err != nil {
		return nil, fmt.Errorf("open aria2 log: %w", err)
	}
	// The secret lives only in the conf file; argv is world-readable in /proc.
	cmd := exec.Command(sv.s.Engine.Aria2cPath,
		"--conf-path="+sv.p.AriaConf, "--stop-with-process="+strconv.Itoa(os.Getpid()))
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = sysProcAttr()
	pr := &proc{cmd: cmd, port: port, secret: secret, logFrom: fi.Size(), exited: make(chan struct{})}

	// Holding mu orders this against Shutdown's kill of the current process.
	sv.mu.Lock()
	if err = sv.ctx.Err(); err == nil {
		err = cmd.Start()
	}
	if err == nil {
		sv.cur = pr
	}
	sv.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("start aria2c %q: %w (set engine.aria2c_path or run `lazyaria doctor`)", sv.s.Engine.Aria2cPath, err)
	}
	pr.started = time.Now()
	go func() {
		pr.err = cmd.Wait()
		pr.exitedAt = time.Now()
		close(pr.exited)
	}()
	return pr, nil
}

// waitReady polls getVersion until aria2 answers, exits, or readyTimeout passes.
func (sv *Supervisor) waitReady(pr *proc) (*aria2.Client, error) {
	url := fmt.Sprintf("ws://127.0.0.1:%d/jsonrpc", pr.port)
	deadline := time.Now().Add(readyTimeout)
	tick := time.NewTicker(readyPoll)
	defer tick.Stop()
	for {
		if c := sv.tryReady(url, pr.secret); c != nil {
			return c, nil
		}
		// aria2 keeps running when only the IPv4 bind fails, so check the
		// log rather than waiting for an exit.
		if sv.bindFailed(pr) {
			return nil, errBindRace
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("aria2c did not answer on port %d within %s; see %s or run `lazyaria doctor`", pr.port, readyTimeout, sv.logPath())
		}
		select {
		case <-pr.exited:
			if sv.bindFailed(pr) {
				return nil, errBindRace
			}
			return nil, fmt.Errorf("aria2c exited during startup (%v); see %s or run `lazyaria doctor`", pr.err, sv.logPath())
		case <-sv.ctx.Done():
			return nil, sv.ctx.Err()
		case <-tick.C:
		}
	}
}

func (sv *Supervisor) tryReady(url, secret string) *aria2.Client {
	ctx, cancel := context.WithTimeout(sv.ctx, 500*time.Millisecond)
	defer cancel()
	c, err := aria2.Dial(ctx, url, secret, aria2.Options{})
	if err != nil {
		return nil
	}
	if err := c.Call(ctx, "aria2.getVersion", nil, nil); err != nil {
		c.Close()
		return nil
	}
	return c
}

func (sv *Supervisor) bindFailed(pr *proc) bool {
	b := readFrom(sv.logPath(), pr.logFrom)
	return bytes.Contains(b, []byte("Address already in use")) || bytes.Contains(b, []byte("Failed to bind"))
}

// stop asks aria2 to save and quit, escalating if it does not.
func (sv *Supervisor) stop(pr *proc, c *aria2.Client) {
	call := func(method string) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		c.Call(ctx, method, nil, nil)
	}
	call("aria2.shutdown")
	if pr.waitFor(3 * time.Second) {
		return
	}
	call("aria2.forceShutdown")
	if pr.waitFor(5 * time.Second) {
		return
	}
	pr.cmd.Process.Signal(syscall.SIGTERM)
	if pr.waitFor(2 * time.Second) {
		return
	}
	pr.cmd.Process.Kill()
	<-pr.exited
}

// readFrom returns up to the last 64 KiB of path starting at offset.
func readFrom(path string, offset int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil {
		offset = max(offset, fi.Size()-64<<10)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}
	b, _ := io.ReadAll(f)
	return b
}

func (sv *Supervisor) logTail() []string {
	lines := strings.Split(strings.TrimRight(string(readFrom(sv.logPath(), 0)), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines[max(0, len(lines)-tailLines):]
}
