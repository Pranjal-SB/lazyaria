package ipc

import (
	"context"
	"encoding/json"
	"net"
	"sync"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

// Client talks to the daemon. Pushed events and progress arrive on the
// channels Subscribe returns; both close when the connection ends.
type Client struct {
	rpc      *jrpc2.Client
	events   chan daemon.Event
	progress chan []daemon.ProgressItem
}

// Dial connects to a running daemon.
func Dial(ctx context.Context, p config.Paths) (*Client, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", p.Socket)
	if err != nil {
		return nil, err
	}
	c := &Client{events: make(chan daemon.Event, 512), progress: make(chan []daemon.ProgressItem, 1)}
	c.rpc = jrpc2.NewClient(&pushChannel{Channel: lineChannel(conn), c: c}, nil)
	return c, nil
}

func (c *Client) Close() error { return c.rpc.Close() }

// Subscribe asks for job events, and progress frames when progress is set.
func (c *Client) Subscribe(ctx context.Context, progress bool) (<-chan daemon.Event, <-chan []daemon.ProgressItem, error) {
	if err := c.call(ctx, "jobs.subscribe", SubscribeParams{Progress: progress}, nil); err != nil {
		return nil, nil, err
	}
	return c.events, c.progress, nil
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	if result == nil {
		result = new(json.RawMessage)
	}
	return fromWire(c.rpc.CallResult(ctx, method, params, result))
}

// pushChannel takes server notifications off the wire before jrpc2 sees
// them. jrpc2 hands each one to its own goroutine, which can reorder them;
// here they are delivered in arrival order on the reader goroutine.
type pushChannel struct {
	channel.Channel
	c    *Client
	once sync.Once
}

func (p *pushChannel) Recv() ([]byte, error) {
	for {
		b, err := p.Channel.Recv()
		if err != nil {
			p.once.Do(func() { close(p.c.events); close(p.c.progress) })
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
		p.c.deliver(m.Method, m.Params)
	}
}

// deliver queues a push without ever blocking the reader. Events mirror
// the daemon's bus: on overflow the backlog becomes one resync_required.
// Progress keeps only the newest frame.
func (c *Client) deliver(method string, params json.RawMessage) {
	switch method {
	case PushEvent:
		var ev daemon.Event
		if json.Unmarshal(params, &ev) != nil {
			return
		}
		select {
		case c.events <- ev:
			return
		default:
		}
		for len(c.events) > 0 {
			select {
			case <-c.events:
			default:
			}
		}
		c.events <- daemon.Event{Seq: ev.Seq, Type: "resync_required"}
	case PushProgress:
		var items []daemon.ProgressItem
		if json.Unmarshal(params, &items) != nil {
			return
		}
		select {
		case <-c.progress:
		default:
		}
		c.progress <- items
	}
}
