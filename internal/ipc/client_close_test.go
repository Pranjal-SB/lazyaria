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

// Close does not wait for a redial under way; it cancels it.
func TestCloseCancelsRedial(t *testing.T) {
	h := &heldSnapshot{fakeHandler: newFake()}
	c, _, stop := serveStoppable(t, h)
	started := make(chan struct{})
	c.Redial = func(ctx context.Context) (*Client, error) {
		close(started)
		<-ctx.Done() // a daemon that never comes back
		return nil, ctx.Err()
	}
	errc := make(chan error, 1)
	go func() { _, err := c.List(context.Background(), false, 0); errc <- err }()
	for h.waiting.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	stop()
	<-started
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close waited for the redial")
	}
	select {
	case <-errc:
	case <-time.After(2 * time.Second):
		t.Fatal("the call did not end after Close")
	}
}

// The redial serves every call that lost the connection, so the caller
// that started it giving up does not leave the client without one.
func TestRedialOutlivesItsCaller(t *testing.T) {
	_, pB := serve(t, newFake())
	h := &heldSnapshot{fakeHandler: newFake()}
	c, _, stop := serveStoppable(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	c.Redial = func(rctx context.Context) (*Client, error) {
		cancel() // the caller that hit the hang-up gives up
		return Dial(rctx, pB)
	}
	errc := make(chan error, 1)
	go func() { _, err := c.List(ctx, false, 0); errc <- err }()
	for h.waiting.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	stop()
	<-errc
	if _, err := c.List(context.Background(), false, 0); err != nil {
		t.Fatalf("call after the redial: %v", err)
	}
}

// serveStoppable is serve with a function that stops the server early.
func serveStoppable(t *testing.T, h Handler) (*Client, string, func()) {
	t.Helper()
	p := testPaths(shortDir(t))
	l, lock, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, h) }()
	t.Cleanup(func() { cancel(); <-done; lock.Close() })
	c, err := Dial(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, p.Socket, cancel
}
