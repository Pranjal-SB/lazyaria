package aria2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

var ErrDisconnected = errors.New("aria2: disconnected")

type Options struct {
	NotifyBuffer         int           // default 256
	RedialMin, RedialMax time.Duration // default 100ms, 2s
}

type Notification struct{ Method, GID string }

type Call struct {
	Method string
	Params []any
}

type result struct {
	raw json.RawMessage
	err error
}

// session is one live websocket connection. Only its writer goroutine writes.
type session struct {
	conn *websocket.Conn
	send chan []byte
	done chan struct{}
}

type Client struct {
	url, secret string
	opt         Options
	ctx         context.Context
	cancel      context.CancelFunc
	finished    chan struct{}
	nextID      atomic.Uint64
	notify      chan Notification
	resync      chan struct{}

	mu      sync.Mutex
	sess    *session
	pending map[string]chan result
}

// Dial connects once (failing fast) and then keeps the connection alive,
// redialling with backoff until Close.
func Dial(ctx context.Context, url, secret string, opt Options) (*Client, error) {
	if opt.NotifyBuffer <= 0 {
		opt.NotifyBuffer = 256
	}
	if opt.RedialMin <= 0 {
		opt.RedialMin = 100 * time.Millisecond
	}
	if opt.RedialMax < opt.RedialMin {
		opt.RedialMax = max(2*time.Second, opt.RedialMin)
	}
	s, err := connect(ctx, url)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		url: url, secret: secret, opt: opt, ctx: cctx, cancel: cancel,
		finished: make(chan struct{}),
		notify:   make(chan Notification, opt.NotifyBuffer),
		resync:   make(chan struct{}, 1),
		pending:  map[string]chan result{},
		sess:     s,
	}
	go c.run(s)
	return c, nil
}

func connect(ctx context.Context, url string) (*session, error) {
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("aria2: cannot connect to %s: %w (is the engine running? see lazyaria doctor)", url, err)
	}
	conn.SetReadLimit(16 << 20) // tellActive with many files is big
	return &session{conn: conn, send: make(chan []byte), done: make(chan struct{})}, nil
}

func (c *Client) Notifications() <-chan Notification { return c.notify }

// Resync fires after a reconnect or a dropped notification: refetch state.
func (c *Client) Resync() <-chan struct{} { return c.resync }

func (c *Client) Close() error {
	c.cancel()
	<-c.finished
	return nil
}

func (c *Client) run(s *session) {
	defer close(c.finished)
	defer close(c.notify)
	defer close(c.resync)
	for {
		c.serve(s)
		if c.ctx.Err() != nil {
			return
		}
		if s = c.redial(); s == nil {
			return
		}
		c.signalResync()
	}
}

func (c *Client) signalResync() {
	select {
	case c.resync <- struct{}{}:
	default:
	}
}

func (c *Client) redial() *session {
	wait := c.opt.RedialMin
	for {
		t := time.NewTimer(wait)
		select {
		case <-c.ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		if s, err := connect(c.ctx, c.url); err == nil {
			c.mu.Lock()
			c.sess = s
			c.mu.Unlock()
			return s
		}
		wait = min(wait*2, c.opt.RedialMax)
	}
}

// serve runs the read loop for one session and tears it down when it ends.
func (c *Client) serve(s *session) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case b := <-s.send:
				if err := s.conn.Write(c.ctx, websocket.MessageText, b); err != nil {
					s.conn.CloseNow()
					return
				}
			case <-s.done:
				return
			}
		}
	}()
	for {
		_, b, err := s.conn.Read(c.ctx)
		if err != nil {
			break
		}
		c.dispatch(b)
	}
	c.mu.Lock()
	c.sess = nil
	failed := c.pending
	c.pending = map[string]chan result{}
	c.mu.Unlock()
	for _, ch := range failed {
		ch <- result{err: ErrDisconnected}
	}
	close(s.done)
	s.conn.CloseNow()
	wg.Wait()
}

type message struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
	Result json.RawMessage   `json:"result"`
	Error  *RPCError         `json:"error"`
}

func (c *Client) dispatch(b []byte) {
	var m message
	if json.Unmarshal(b, &m) != nil {
		return
	}
	var id string
	if len(m.ID) > 0 {
		json.Unmarshal(m.ID, &id)
	}
	if id == "" {
		if m.Method == "" {
			return
		}
		n := Notification{Method: m.Method}
		if len(m.Params) > 0 {
			var p struct {
				GID string `json:"gid"`
			}
			json.Unmarshal(m.Params[0], &p)
			n.GID = p.GID
		}
		select {
		case c.notify <- n:
		default:
			c.signalResync()
		}
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if m.Error != nil {
		ch <- result{err: m.Error}
		return
	}
	ch <- result{raw: m.Result}
}

// Call invokes an aria2 method, adding the secret token as first param.
// A call cancelled after it was sent may still run on aria2, so always pass a
// context with a deadline and treat a timeout as "outcome unknown".
func (c *Client) Call(ctx context.Context, method string, params []any, out any) error {
	return c.call(ctx, method, c.withToken(params), out)
}

func (c *Client) call(ctx context.Context, method string, params []any, out any) error {
	if params == nil {
		params = []any{}
	}
	c.mu.Lock()
	s := c.sess
	if s == nil {
		c.mu.Unlock()
		return ErrDisconnected
	}
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan result, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		forget()
		return err
	}
	select {
	case s.send <- b:
	case <-s.done:
		forget()
		return ErrDisconnected
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(r.raw, out)
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
}
