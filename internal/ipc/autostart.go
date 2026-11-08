package ipc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
// here both start a daemon; the daemon lock lets only one of them live.
func Connect(ctx context.Context, p config.Paths, autoStart bool, exe string) (*Client, error) {
	c, err := Dial(ctx, p)
	if err == nil {
		return c, nil
	}
	if !autoStart {
		return nil, fmt.Errorf("%w (%v)", ErrUnreachable, err)
	}
	cmd := exec.Command(exe, "daemon", "--detach")
	if serr := cmd.Start(); serr != nil {
		return nil, fmt.Errorf("%w (start daemon: %v)", ErrUnreachable, serr)
	}
	go cmd.Wait()

	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	tick := time.NewTicker(startPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (%v)", ErrUnreachable, err)
		case <-tick.C:
		}
		if c, err = Dial(ctx, p); err == nil {
			return c, nil
		}
	}
}
