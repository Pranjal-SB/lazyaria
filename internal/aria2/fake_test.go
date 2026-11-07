package aria2

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

type rpcReq struct {
	ID     string            `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

// fakeServer is a minimal aria2 stand-in speaking JSON-RPC over a websocket.
type fakeServer struct {
	addr  string
	srv   *http.Server
	mu    sync.Mutex
	conns []*websocket.Conn
	wg    sync.WaitGroup
}

func startFake(t *testing.T, addr string, h func(ctx context.Context, c *websocket.Conn)) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{addr: ln.Addr().String()}
	f.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, c)
		f.mu.Unlock()
		f.wg.Add(1)
		defer f.wg.Done()
		h(r.Context(), c)
		c.CloseNow()
	})}
	go f.srv.Serve(ln)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) URL() string { return "ws://" + f.addr + "/jsonrpc" }

func (f *fakeServer) Close() {
	f.srv.Close()
	f.mu.Lock()
	for _, c := range f.conns {
		c.CloseNow()
	}
	f.mu.Unlock()
	f.wg.Wait()
}

func readReq(ctx context.Context, c *websocket.Conn) (rpcReq, error) {
	var r rpcReq
	_, b, err := c.Read(ctx)
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

func reply(ctx context.Context, c *websocket.Conn, id, result string) error {
	return c.Write(ctx, websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":"`+id+`","result":`+result+`}`))
}

// okServer answers every request with "OK".
func okServer(t *testing.T) *fakeServer {
	return startFake(t, "127.0.0.1:0", func(ctx context.Context, c *websocket.Conn) {
		for {
			r, err := readReq(ctx, c)
			if err != nil {
				return
			}
			reply(ctx, c, r.ID, `"OK"`)
		}
	})
}
