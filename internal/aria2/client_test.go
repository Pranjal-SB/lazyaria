package aria2

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func dial(t *testing.T, url string, opt Options) *Client {
	t.Helper()
	c, err := Dial(context.Background(), url, "s3cret", opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestCallPrependsToken(t *testing.T) {
	f := startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		r, err := readReq(ctx, c)
		if err != nil {
			return
		}
		if len(r.Params) < 2 || string(r.Params[0]) != `"token:s3cret"` || r.Method != "aria2.getVersion" {
			reply(ctx, c, r.ID, `"BAD"`)
			return
		}
		reply(ctx, c, r.ID, `"OK"`)
	})
	c := dial(t, f.URL(), Options{})
	var out string
	if err := c.Call(context.Background(), "aria2.getVersion", []any{"x"}, &out); err != nil {
		t.Fatal(err)
	}
	if out != "OK" {
		t.Fatalf("got %q", out)
	}
}

func TestOutOfOrderResponses(t *testing.T) {
	f := startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		a, err := readReq(ctx, c)
		if err != nil {
			return
		}
		b, err := readReq(ctx, c)
		if err != nil {
			return
		}
		reply(ctx, c, b.ID, string(b.Params[1]))
		reply(ctx, c, a.ID, string(a.Params[1]))
		c.Read(ctx)
	})
	c := dial(t, f.URL(), Options{})
	res := make(chan [2]int, 2)
	for i := 1; i <= 2; i++ {
		go func() {
			var out int
			if err := c.Call(context.Background(), "m", []any{i}, &out); err != nil {
				out = -1
			}
			res <- [2]int{i, out}
		}()
		time.Sleep(20 * time.Millisecond)
	}
	for range 2 {
		if r := <-res; r[0] != r[1] {
			t.Fatalf("call %d got %d", r[0], r[1])
		}
	}
}

func TestNotificationRouting(t *testing.T) {
	f := startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		for {
			r, err := readReq(ctx, c)
			if err != nil {
				return
			}
			// push the notification while the call is in flight, then answer it.
			c.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","method":"aria2.onDownloadComplete","params":[{"gid":"abc"}]}`))
			reply(ctx, c, r.ID, `"OK"`)
		}
	})
	c := dial(t, f.URL(), Options{})
	var out string
	if err := c.Call(context.Background(), "m", nil, &out); err != nil || out != "OK" {
		t.Fatalf("%v %q", err, out)
	}
	select {
	case n := <-c.Notifications():
		if n != (Notification{Method: "aria2.onDownloadComplete", GID: "abc"}) {
			t.Fatalf("got %+v", n)
		}
	case <-time.After(time.Second):
		t.Fatal("no notification")
	}
}

func TestMulticallMixedResults(t *testing.T) {
	f := startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		r, err := readReq(ctx, c)
		if err != nil {
			return
		}
		reply(ctx, c, r.ID, `[["a"],{"code":1,"message":"GID not found"},["c"]]`)
		c.Read(ctx)
	})
	c := dial(t, f.URL(), Options{})
	res, err := c.Multicall(context.Background(), make([]Call, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || string(res[0].Raw) != `"a"` || res[0].Err != nil ||
		res[1].Err == nil || res[1].Err.Code != 1 || res[1].Err.Message != "GID not found" || res[1].Raw != nil ||
		string(res[2].Raw) != `"c"` || res[2].Err != nil {
		t.Fatalf("%+v", res)
	}
}

func TestMulticallTokens(t *testing.T) {
	got := make(chan []byte, 1)
	f := startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		_, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		got <- b
		var r rpcReq
		json.Unmarshal(b, &r)
		reply(ctx, c, r.ID, `[["a"],["b"]]`)
		c.Read(ctx)
	})
	c := dial(t, f.URL(), Options{})
	res, err := c.Multicall(context.Background(), []Call{
		{Method: "aria2.tellStatus", Params: []any{"g1"}},
		{Method: "aria2.tellActive"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || string(res[0].Raw) != `"a"` || string(res[1].Raw) != `"b"` {
		t.Fatalf("res %s", res)
	}
	raw := <-got
	var req struct {
		Method string `json:"method"`
		Params [][]struct {
			MethodName string `json:"methodName"`
			Params     []any  `json:"params"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if req.Method != "system.multicall" || len(req.Params) != 1 || len(req.Params[0]) != 2 {
		t.Fatalf("bad request %s", raw)
	}
	for _, s := range req.Params[0] {
		if len(s.Params) == 0 || s.Params[0] != "token:s3cret" {
			t.Fatalf("sub-call without token: %s", raw)
		}
	}
}

func TestDisconnectFailsPending(t *testing.T) {
	gotReq := make(chan struct{})
	f := startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		if _, err := readReq(ctx, c); err == nil {
			close(gotReq)
		}
		c.Read(ctx)
	})
	c := dial(t, f.URL(), Options{RedialMin: 10 * time.Millisecond, RedialMax: 50 * time.Millisecond})
	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "m", nil, nil) }()
	<-gotReq
	addr := f.addr
	start := time.Now()
	f.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrDisconnected) {
			t.Fatalf("err %v", err)
		}
		if d := time.Since(start); d > 100*time.Millisecond {
			t.Fatalf("took %v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("pending call not failed")
	}
	// bring the server back on the same address.
	startFake(t, addr, func(ctx context.Context, cn *websocket.Conn) {
		for {
			r, err := readReq(ctx, cn)
			if err != nil {
				return
			}
			reply(ctx, cn, r.ID, `"OK"`)
		}
	})
	select {
	case <-c.Resync():
	case <-time.After(2 * time.Second):
		t.Fatal("no resync")
	}
	var out string
	if err := c.Call(context.Background(), "m", nil, &out); err != nil || out != "OK" {
		t.Fatalf("%v %q", err, out)
	}
}

func TestNotifyOverflowResync(t *testing.T) {
	f := startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		for range 5 {
			c.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","method":"aria2.onDownloadStart","params":[{"gid":"g"}]}`))
		}
		c.Read(ctx)
	})
	c := dial(t, f.URL(), Options{NotifyBuffer: 2})
	select {
	case <-c.Resync():
	case <-time.After(time.Second):
		t.Fatal("no resync on overflow")
	}
}

func TestRPCError(t *testing.T) {
	f := startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		r, err := readReq(ctx, c)
		if err != nil {
			return
		}
		c.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":"`+r.ID+`","error":{"code":1,"message":"Unauthorized"}}`))
		c.Read(ctx)
	})
	c := dial(t, f.URL(), Options{})
	err := c.Call(context.Background(), "m", nil, nil)
	var re *RPCError
	if !errors.As(err, &re) || re.Code != 1 || re.Message != "Unauthorized" {
		t.Fatalf("err %v", err)
	}
}

func TestCallContextCancel(t *testing.T) {
	f := startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	})
	c := dial(t, f.URL(), Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := c.Call(ctx, "m", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
}

func TestCloseNoLeak(t *testing.T) {
	f := okServer(t)
	time.Sleep(20 * time.Millisecond)
	before := runtime.NumGoroutine()
	c, err := Dial(context.Background(), f.URL(), "s3cret", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "m", nil, nil); err != nil {
		t.Fatal(err)
	}
	c.Close()
	f.Close()
	for i := 0; i < 100 && runtime.NumGoroutine() > before; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines %d > %d\n%s", n, before, buf[:runtime.Stack(buf, true)])
	}
	if err := c.Call(context.Background(), "m", nil, nil); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("call after close: %v", err)
	}
}
