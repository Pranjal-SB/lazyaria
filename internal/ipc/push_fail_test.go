package ipc

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

// A push that cannot be encoded must drop the connection, so the client
// sees a disconnect and resyncs instead of waiting on a dead pusher.
func TestPushFailureClosesConnection(t *testing.T) {
	f := newFake()
	p := testPaths(shortDir(t))
	l, lock, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, l, f)

	raw, err := net.Dial("unix", p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"jobs.subscribe","params":{"progress":false}}` + "\n"))
	r := bufio.NewReader(raw)
	raw.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := r.ReadBytes('\n'); err != nil {
		t.Fatalf("subscribe reply: %v", err)
	}
	f.bus.Publish(daemon.Event{Type: "banner", Data: map[string]any{"bad": make(chan int)}})
	if _, err := r.ReadBytes('\n'); err == nil || isTimeout(err) {
		t.Fatalf("want the connection closed, got %v", err)
	}
}

// A push failing while its client keeps sending must not crash the daemon:
// jrpc2 panics when Stop races a request it is reading.
func TestPushFailureWhileClientsSend(t *testing.T) {
	f := newFake()
	p := testPaths(shortDir(t))
	l, lock, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, l, f)

	sub := []byte(`{"jsonrpc":"2.0","id":1,"method":"jobs.subscribe","params":{"progress":false}}` + "\n")
	hello := []byte(`{"jsonrpc":"2.0","id":2,"method":"hello","params":{"client":"t","version":"dev"}}` + "\n")
	for range 30 {
		var conns []net.Conn
		for range 20 {
			c, err := net.Dial("unix", p.Socket)
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, c)
			c.Write(sub)
			go io.Copy(io.Discard, c)
			go func() {
				for {
					if _, err := c.Write(hello); err != nil {
						return
					}
				}
			}()
		}
		time.Sleep(5 * time.Millisecond)
		f.bus.Publish(daemon.Event{Type: "banner", Data: map[string]any{"bad": make(chan int)}})
		time.Sleep(5 * time.Millisecond)
		for _, c := range conns {
			c.Close()
		}
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
