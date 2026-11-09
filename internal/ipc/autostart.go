package ipc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
)

// ErrUnreachable means no daemon answered and none could be started. The
// CLI exits with code 3 on it.
var ErrUnreachable = errors.New("cannot reach the lazyaria daemon; run `lazyaria doctor`")

const (
	startPoll    = 20 * time.Millisecond
	startTimeout = 3 * time.Second
)

// Connect dials the daemon. If none is running and autoStart is set, it
// runs `exe daemon --detach` and waits for the socket. Two clients racing
// here both start a daemon; the daemon lock lets only one of them live,
// and the other exits 0. A non-zero exit means the daemon could not start,
// and its stderr (say, how to install aria2) is passed on to the user.
func Connect(ctx context.Context, p config.Paths, autoStart bool, exe string) (*Client, error) {
	c, err := Dial(ctx, p)
	if err == nil {
		return c, nil
	}
	if _, foreign := errors.AsType[*ErrForeignDaemon](err); foreign || !autoStart {
		return nil, fmt.Errorf("%w (%w)", ErrUnreachable, err)
	}
	var stderr tailBuf
	cmd := exec.Command(exe, "daemon", "--detach")
	cmd.Stderr = &stderr
	cmd.WaitDelay = 100 * time.Millisecond // a detached daemon must not keep Wait open
	if serr := cmd.Start(); serr != nil {
		return nil, fmt.Errorf("%w (start daemon: %v)", ErrUnreachable, serr)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	tick := time.NewTicker(startPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (%v)", ErrUnreachable, err)
		case werr := <-exited:
			exited = nil
			if werr != nil {
				return nil, fmt.Errorf("%w: the daemon exited (%v): %s", ErrUnreachable, werr, stderr.String())
			}
		case <-tick.C:
		}
		if c, err = Dial(ctx, p); err == nil {
			return c, nil
		}
	}
}

// tailBuf keeps the last 2 KiB written to it.
type tailBuf struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 2048 {
		t.b = t.b[len(t.b)-2048:]
	}
	return len(p), nil
}

func (t *tailBuf) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
}
