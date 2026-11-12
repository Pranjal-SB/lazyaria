package ipc

import (
	"bufio"
	"context"
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

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
