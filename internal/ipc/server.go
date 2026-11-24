package ipc

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

// maxCallsPerConn bounds the handlers one client may run at once. jrpc2's
// default (NumCPU) lets a few slow calls, like a cross-filesystem trash, hold
// up the client's next read; the peer is our own user, so a high cap is safe.
const maxCallsPerConn = 64

type server struct {
	h Handler

	mu        sync.Mutex
	observers int // clients subscribed with progress
	clients   int
}

// Serve accepts clients on l until ctx ends. Only processes running as the
// daemon's own user are served; anyone else is hung up on.
func Serve(ctx context.Context, l net.Listener, h Handler) error {
	s := &server{h: h}
	var wg sync.WaitGroup
	defer wg.Wait()
	// Cancelling on return hangs up on every client before the wait above.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	context.AfterFunc(ctx, func() { l.Close() })

	var backoff time.Duration
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			// Out of descriptors and the like: wait for clients to leave
			// rather than stop serving.
			backoff = min(max(2*backoff, 5*time.Millisecond), time.Second)
			slog.Warn("ipc: accept failed, retrying", "err", err, "in", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			continue
		}
		backoff = 0
		uid, err := peerUID(c)
		if err != nil || uid != os.Geteuid() {
			slog.Warn("ipc: rejected connection from another user", "uid", uid, "err", err)
			c.Close()
			continue
		}
		wg.Go(func() { s.serveConn(ctx, c) })
	}
}

func (s *server) serveConn(ctx context.Context, c net.Conn) {
	s.connected(1)
	defer s.connected(-1)
	cc := &conn{s: s, sock: c}
	srv := jrpc2.NewServer(cc.methods(), &jrpc2.ServerOptions{AllowPush: true, Concurrency: maxCallsPerConn})
	cc.srv = srv
	srv.Start(lineChannel(deadlineConn{c}))
	// Closing the socket ends the server from its own reader, which also
	// unblocks a push stuck writing to a client that stopped reading.
	// srv.Stop here would race a request being read and panic in jrpc2.
	stop := context.AfterFunc(ctx, func() { c.Close() })
	srv.Wait()
	stop()
	cc.unsubscribe()
}

func (s *server) connected(delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients += delta
	s.h.SetClients(s.clients)
}

func (s *server) observe(delta int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observers += delta
	s.h.SetObservers(s.observers)
}

// conn is one client's session.
type conn struct {
	s    *server
	srv  *jrpc2.Server
	sock net.Conn

	mu       sync.Mutex // held across (un)subscribe so they never interleave
	sub      *daemon.Sub
	progress bool
	pushed   chan struct{} // closed when the pusher for sub exits
}

func (c *conn) subscribe(progress bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unsubscribeLocked()
	sub := c.s.h.Subscribe(progress)
	c.sub, c.progress, c.pushed = sub, progress, make(chan struct{})
	if progress {
		c.s.observe(1)
	}
	go c.push(sub, c.pushed)
}

// push forwards sub to the client until sub is closed or the client goes.
func (c *conn) push(sub *daemon.Sub, done chan struct{}) {
	defer close(done)
	evs, prog := sub.Events(), sub.Progress()
	for {
		var err error
		select {
		case ev, ok := <-evs:
			if !ok {
				return
			}
			ev.Job = redact(ev.Job)
			err = c.srv.Notify(context.Background(), PushEvent, ev)
		case items, ok := <-prog:
			if !ok {
				return
			}
			err = c.srv.Notify(context.Background(), PushProgress, items)
		}
		if err != nil {
			if !errors.Is(err, jrpc2.ErrConnClosed) {
				slog.Warn("ipc: push failed, dropping the client", "err", err)
			}
			// Hang up so the client sees a disconnect and resyncs. Closing
			// the socket ends the server from its reader; srv.Stop would
			// race a request being read and panic in jrpc2 (see serveConn).
			c.sock.Close()
			return
		}
	}
}

func (c *conn) unsubscribe() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unsubscribeLocked()
}

func (c *conn) unsubscribeLocked() {
	if c.sub == nil {
		return
	}
	c.sub.Close()
	<-c.pushed
	c.sub = nil
	if c.progress {
		c.s.observe(-1)
	}
}
