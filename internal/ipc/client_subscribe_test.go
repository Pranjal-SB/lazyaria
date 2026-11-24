package ipc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
)

// A call that lost the connection can start a redial between the
// subscribe reply and Subscribe taking the lock. Subscribe must then
// subscribe again on the new connection, not hand back dead channels.
func TestSubscribeWaitsForRedialUnderWay(t *testing.T) {
	c, _, stopA := serveStoppable(t, newFake())
	fB := newFake()
	_, pB := serve(t, fB)
	var redials atomic.Int32
	release := make(chan struct{})
	c.Redial = func(ctx context.Context) (*Client, error) {
		redials.Add(1)
		<-release
		return Dial(ctx, pB)
	}

	listed := make(chan error, 1)
	old := afterSubscribeCall
	t.Cleanup(func() { afterSubscribeCall = old })
	afterSubscribeCall = sync.OnceFunc(func() {
		stopA()
		c.mu.Lock()
		on := c.rpc
		c.mu.Unlock()
		for deadline := time.Now().Add(5 * time.Second); !on.IsStopped(); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Error("old connection never ended")
				break
			}
		}
		go func() {
			_, err := c.List(context.Background(), false, 0)
			listed <- err
		}()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			c.mu.Lock()
			under := c.redialing != nil
			c.mu.Unlock()
			if under {
				break
			}
			if time.Now().After(deadline) {
				t.Error("no redial started")
				break
			}
		}
		// Released late so Subscribe takes the lock while the redial is
		// still under way.
		time.AfterFunc(50*time.Millisecond, func() { close(release) })
	})

	evs, _, err := c.Subscribe(context.Background(), false)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := <-listed; err != nil {
		t.Errorf("List across the redial: %v", err)
	}
	fB.bus.Publish(daemon.Event{Type: "job.added"})
	select {
	case ev, ok := <-evs:
		if !ok {
			t.Fatal("events closed: Subscribe kept the old connection")
		}
		if ev.Type != "job.added" {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event from the new server")
	}
	if got := redials.Load(); got != 1 {
		t.Errorf("redialled %d times, want 1", got)
	}
}

// Once subscribed, a hang-up ends the client: the events died with the
// connection, so it must not quietly reconnect.
func TestNoRedialAfterSubscribe(t *testing.T) {
	c, _, stopA := serveStoppable(t, newFake())
	_, pB := serve(t, newFake())
	var redials atomic.Int32
	c.Redial = func(ctx context.Context) (*Client, error) {
		redials.Add(1)
		return Dial(ctx, pB)
	}
	evs, prog, err := c.Subscribe(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	stopA()
	for name, closed := range map[string]func() bool{
		"events":   func() bool { _, ok := <-evs; return !ok },
		"progress": func() bool { _, ok := <-prog; return !ok },
	} {
		done := make(chan bool, 1)
		go func() { done <- closed() }()
		select {
		case ok := <-done:
			if !ok {
				t.Errorf("%s got a value, want closed", name)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s still open after the hang-up", name)
		}
	}
	_, err = c.List(context.Background(), false, 0)
	if !errors.Is(err, ErrNotSent) && !errors.Is(err, ErrCutOff) {
		t.Errorf("List after hang-up: %v, want ErrNotSent or ErrCutOff", err)
	}
	if got := redials.Load(); got != 0 {
		t.Errorf("redialled %d times, want 0", got)
	}
}
