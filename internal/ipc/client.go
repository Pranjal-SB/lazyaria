package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

// Client talks to the daemon. Pushed events and progress arrive on the
// channels Subscribe returns; both close when the connection ends.
//
// Calls may run at once from several goroutines. mu guards the fields
// below it, so a reconnect is made once and the calls that lost the same
// connection share the new one; it is not held while a call is in flight.
type Client struct {
	mu       sync.Mutex
	rpc      *jrpc2.Client
	events   chan daemon.Event
	progress chan []daemon.ProgressItem

	// Redial, when set, reconnects after the daemon hung up in the middle
	// of a call. A read-only call is sent once more on the new connection;
	// any other fails with ErrCutOff. It is used once, and never after
	// Subscribe: those events died with the old connection. Set it before
	// the client is shared.
	Redial     func(context.Context) (*Client, error)
	subscribed bool
	dead       bool          // closed, or the connection dropped and none replaced it
	redialing  chan struct{} // closed when the redial under way ends
	stopRedial func()        // cancels the redial under way
}

// redialTimeout bounds a reconnect, which no single caller's context ends.
const redialTimeout = 10 * time.Second

var clientPeerUID = peerUID // swapped in tests

// Dial connects to a running daemon.
func Dial(ctx context.Context, p config.Paths) (*Client, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", p.Socket)
	if err != nil {
		return nil, err
	}
	// A socket in a shared dir could be someone else's daemon.
	uid, err := clientPeerUID(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("could not check who runs the daemon at %s: %w; run `lazyaria doctor`", p.Socket, err)
	}
	if uid != os.Geteuid() {
		conn.Close()
		return nil, &ErrForeignDaemon{Socket: p.Socket, UID: uid}
	}
	c := &Client{events: make(chan daemon.Event, 512), progress: make(chan []daemon.ProgressItem, 1)}
	c.rpc = jrpc2.NewClient(&pushChannel{Channel: lineChannel(conn), events: c.events, progress: c.progress}, nil)
	return c, nil
}

// Close ends the connection. A call in flight fails rather than reconnect,
// and a reconnect under way is cancelled, not waited for.
func (c *Client) Close() error {
	c.mu.Lock()
	c.dead = true
	rpc, stop := c.rpc, c.stopRedial
	c.mu.Unlock()
	if stop != nil {
		stop()
	}
	return rpc.Close()
}

// ErrForeignDaemon means the socket is served by another user.
type ErrForeignDaemon struct {
	Socket string
	UID    int
}

func (e *ErrForeignDaemon) Error() string {
	return fmt.Sprintf("%s is served by uid %d, not you; refusing to use it. Pick another advanced.socket_path or run `lazyaria doctor`", e.Socket, e.UID)
}

// Subscribe asks for job events, and progress frames when progress is set.
func (c *Client) Subscribe(ctx context.Context, progress bool) (<-chan daemon.Event, <-chan []daemon.ProgressItem, error) {
	on, err := c.callOn(ctx, "jobs.subscribe", SubscribeParams{Progress: progress}, nil)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rpc != on {
		// Another call reconnected after this one subscribed; the events
		// went with the old connection.
		return nil, nil, ErrNotSent
	}
	c.subscribed = true
	return c.events, c.progress, nil
}

// ErrCutOff means the connection dropped in the middle of a call that
// changes something (the daemon upgraded, crashed or was stopped), so the
// call may have run before it went away.
var ErrCutOff = errors.New("the connection to the daemon dropped during this command, so it may or may not have taken effect; check with `lazyaria list` (or `lazyaria config list`) before running it again")

// ErrNotSent means an earlier call lost the connection and no new one was
// made, so this call never left the client.
var ErrNotSent = errors.New("not sent: the connection to the daemon dropped earlier in this command; run it again (see `lazyaria doctor` if the daemon is down)")

// readOnly are the methods that change nothing in the daemon, so sending one
// again after a hang-up cannot do anything twice. jobs.subscribe only sets
// up the connection it is sent on.
var readOnly = map[string]bool{
	"hello": true, "daemon.status": true, "jobs.snapshot": true, "jobs.search": true,
	"jobs.subscribe": true, "config.get": true, "config.list": true,
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	_, err := c.callOn(ctx, method, params, result)
	return err
}

// callOn is call that also returns the connection the call was answered on.
func (c *Client) callOn(ctx context.Context, method string, params, result any) (*jrpc2.Client, error) {
	rpc := c.current(ctx)
	if rpc == nil {
		return nil, ErrNotSent
	}
	if result == nil {
		result = new(json.RawMessage)
	}
	err := fromWire(rpc.CallResult(ctx, method, params, result))
	if err == nil || ctx.Err() != nil || !hungUp(err) {
		return rpc, err
	}
	resend := readOnly[method]
	if !resend {
		err = ErrCutOff
	}
	// Reconnect either way, so the calls after this one still go out.
	next := c.reconnect(ctx, rpc)
	if next == nil || !resend {
		return nil, err
	}
	return next, fromWire(next.CallResult(ctx, method, params, result))
}

// current waits for a reconnect under way and returns the connection to
// use, or nil when the client is closed or has none.
func (c *Client) current(ctx context.Context) *jrpc2.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.redialing != nil {
		ch := c.redialing
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
		}
		c.mu.Lock()
		if ctx.Err() != nil {
			return nil
		}
	}
	if c.dead {
		return nil
	}
	return c.rpc
}

// reconnect replaces the connection old, which hung up, and returns the new
// one, or nil when there is none. Calls that lost old at the same time wait
// for one reconnect and share it. The lock is not held while redialling, so
// Close never waits for it.
func (c *Client) reconnect(ctx context.Context, old *jrpc2.Client) *jrpc2.Client {
	for {
		if rpc := c.current(ctx); rpc != old {
			return rpc // another call reconnected, or the client is closed
		}
		c.mu.Lock()
		if c.rpc == old && c.redialing == nil {
			break // still holding the lock
		}
		c.mu.Unlock() // another call started a redial meanwhile
	}
	if c.Redial == nil || c.subscribed || c.dead {
		c.dead = true
		c.mu.Unlock()
		return nil
	}
	redial := c.Redial
	c.Redial = nil
	done := make(chan struct{})
	// The redial serves every waiting call, so one caller giving up must
	// not end it.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), redialTimeout)
	c.redialing, c.stopRedial = done, cancel
	c.mu.Unlock()

	nc, err := redial(rctx)
	cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	c.redialing, c.stopRedial = nil, nil
	close(done)
	if err != nil || c.dead {
		if nc != nil {
			nc.Close()
		}
		c.dead = true
		return nil
	}
	c.rpc.Close()
	c.rpc, c.events, c.progress = nc.rpc, nc.events, nc.progress
	return c.rpc
}

// hungUp reports whether err is the connection going away rather than an
// answer: jrpc2 fails pending calls with context.Canceled when the server
// closes, and with an internal error on a reset.
func hungUp(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	e, ok := errors.AsType[*jrpc2.Error](err)
	return ok && e.Code == jrpc2.InternalError && (strings.Contains(e.Message, "connection reset") || strings.Contains(e.Message, "broken pipe"))
}

// pushChannel takes server notifications off the wire before jrpc2 sees
// them. jrpc2 hands each one to its own goroutine, which can reorder them;
// here they are delivered in arrival order on the reader goroutine.
type pushChannel struct {
	channel.Channel
	events   chan daemon.Event
	progress chan []daemon.ProgressItem
	once     sync.Once
}

func (p *pushChannel) Recv() ([]byte, error) {
	for {
		b, err := p.Channel.Recv()
		if err != nil {
			p.once.Do(func() { close(p.events); close(p.progress) })
			return b, err
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &m) != nil || m.Method == "" || m.ID != nil {
			return b, nil
		}
		p.deliver(m.Method, m.Params)
	}
}

// deliver queues a push without ever blocking the reader. Events mirror
// the daemon's bus: on overflow the backlog becomes one resync_required.
// Progress keeps only the newest frame.
func (p *pushChannel) deliver(method string, params json.RawMessage) {
	switch method {
	case PushEvent:
		var ev daemon.Event
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		select {
		case p.events <- ev:
			return
		default:
		}
		for len(p.events) > 0 {
			select {
			case <-p.events:
			default:
			}
		}
		p.events <- daemon.Event{Seq: ev.Seq, Type: "resync_required"}
	case PushProgress:
		var items []daemon.ProgressItem
		if json.Unmarshal(params, &items) != nil {
			return
		}
		select {
		case <-p.progress:
		default:
		}
		p.progress <- items
	}
}
