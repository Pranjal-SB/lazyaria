package ipc

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingFake records every Pause id it receives, on any daemon.
type countingFake struct {
	*fakeHandler
	seen *sync.Map // id -> *atomic.Int32
}

func (h *countingFake) Pause(_ context.Context, ids []string) error {
	for _, id := range ids {
		v, _ := h.seen.LoadOrStore(id, new(atomic.Int32))
		v.(*atomic.Int32).Add(1)
	}
	return nil
}

// Many goroutines share one client while its daemon goes away, and in some
// rounds the client is closed as well. Without a Close every call must
// succeed, or fail with ErrCutOff for a write cut off on the wire (unless a
// subscribe was answered before the hang-up, which ends the client); a write
// is never run twice, ErrNotSent is never said of a write that ran, one
// redial at most is made, and nothing hangs.
func TestClientStress(t *testing.T) {
	const rounds, g = 40, 16
	seen := &sync.Map{}
	pB := testPaths(shortDir(t))
	lB, lockB, err := Listen(pB)
	if err != nil {
		t.Fatal(err)
	}
	defer lockB.Close()
	ctxB, stopB := context.WithCancel(context.Background())
	defer stopB()
	go Serve(ctxB, lB, &countingFake{newFake(), seen})

	for r := range rounds {
		pA := testPaths(shortDir(t))
		lA, lockA, err := Listen(pA)
		if err != nil {
			t.Fatal(err)
		}
		ctxA, stopA := context.WithCancel(context.Background())
		doneA := make(chan error, 1)
		go func() { doneA <- Serve(ctxA, lA, &countingFake{newFake(), seen}) }()
		c, err := Dial(context.Background(), pA)
		if err != nil {
			t.Fatal(err)
		}
		var redials atomic.Int32
		c.Redial = func(ctx context.Context) (*Client, error) {
			redials.Add(1)
			time.Sleep(time.Duration(rand.IntN(3)) * time.Millisecond)
			return Dial(ctx, pB)
		}
		closing := r%5 == 4
		var wg sync.WaitGroup
		type wres struct {
			id  string
			err error
		}
		writes := make(chan wres, g*20)
		type cres struct {
			what string
			err  error
		}
		calls := make(chan cres, g*20)
		check := func(what string, err error) { calls <- cres{what, err} }
		for i := range g {
			wg.Go(func() {
				for k := range 20 {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					switch (i + k) % 4 {
					case 0, 1:
						_, err := c.List(ctx, false, 0)
						check("read", err)
					case 2:
						id := fmt.Sprintf("r%d-g%d-k%d", r, i, k)
						err := c.Pause(ctx, id)
						check("write", err)
						writes <- wres{id, err}
					case 3:
						if k%8 == 3 {
							_, _, err := c.Subscribe(ctx, false)
							check("subscribe", err)
						} else {
							_, err := c.Status(ctx)
							check("read", err)
						}
					}
					cancel()
				}
			})
		}
		time.Sleep(time.Duration(rand.IntN(2000)) * time.Microsecond)
		stopA()
		if closing {
			time.Sleep(time.Duration(rand.IntN(500)) * time.Microsecond)
			c.Close()
		}
		fin := make(chan struct{})
		go func() { wg.Wait(); close(fin) }()
		select {
		case <-fin:
		case <-time.After(20 * time.Second):
			t.Fatalf("round %d: calls stuck", r)
		}
		// A slow machine can answer a subscribe before the hang-up. Its
		// events died with the connection, so the client does not redial
		// (see Client.Redial): later calls are not sent, and calls in
		// flight fail as the hang-up left them.
		c.mu.Lock()
		subscribedOnA := c.subscribed && redials.Load() == 0
		c.mu.Unlock()
		close(calls)
		for cr := range calls {
			switch {
			case closing, cr.err == nil, cr.what == "write" && errors.Is(cr.err, ErrCutOff):
			case subscribedOnA && (errors.Is(cr.err, ErrNotSent) || cr.what != "write" && hungUp(cr.err)):
			default:
				t.Errorf("round %d: %s: %v", r, cr.what, cr.err)
			}
		}
		<-doneA
		lockA.Close()
		c.Close()
		if n := redials.Load(); n > 1 {
			t.Errorf("round %d: %d redials", r, n)
		}
		close(writes)
		for w := range writes {
			n := int32(0)
			if v, ok := seen.Load(w.id); ok {
				n = v.(*atomic.Int32).Load()
			}
			if n > 1 {
				t.Errorf("write %s ran %d times", w.id, n)
			}
			if errors.Is(w.err, ErrNotSent) && n > 0 {
				t.Errorf("write %s ran but was reported not sent", w.id)
			}
			if w.err == nil && n != 1 {
				t.Errorf("write %s ok but ran %d times", w.id, n)
			}
		}
	}
}
