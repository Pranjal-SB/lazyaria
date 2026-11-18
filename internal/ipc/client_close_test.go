package ipc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// Close during a call ends the call; it does not make it redial and open a
// connection (or start a daemon) nobody closes.
func TestCloseDuringCallDoesNotRedial(t *testing.T) {
	_, pB := serve(t, newFake())
	h := &heldSnapshot{fakeHandler: newFake()}
	c, _ := serve(t, h)
	var redials atomic.Int32
	c.Redial = func(ctx context.Context) (*Client, error) {
		redials.Add(1)
		return Dial(ctx, pB)
	}
	errc := make(chan error, 1)
	go func() { _, err := c.List(context.Background(), false, 0); errc <- err }()
	for h.waiting.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	c.Close()
	if err := <-errc; err == nil {
		t.Error("call in flight succeeded after Close")
	}
	if n := redials.Load(); n > 0 {
		t.Errorf("Close made the call in flight redial %d times", n)
	}
}
