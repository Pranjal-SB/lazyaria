package ipc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
)

// undecodable is a result that cannot be decoded, and fails only once
// the daemon that answered has hung up.
type undecodable struct {
	stop func()
	rpc  *jrpc2.Client
}

func (u *undecodable) UnmarshalJSON([]byte) error {
	u.stop()
	for deadline := time.Now().Add(5 * time.Second); !u.rpc.IsStopped() && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	return errors.New("cannot decode this answer")
}

// A write the daemon answered was sent, even if the answer cannot be
// decoded and the connection drops before the client looks at it.
func TestUndecodableAnswerNotResent(t *testing.T) {
	seen := &sync.Map{}
	_, pB := serve(t, &countingFake{newFake(), seen})
	c, _, stop := serveStoppable(t, &countingFake{newFake(), seen})
	c.Redial = func(ctx context.Context) (*Client, error) { return Dial(ctx, pB) }
	err := c.call(context.Background(), "jobs.pause", IDsParams{IDs: []string{"x"}}, &undecodable{stop: stop, rpc: c.rpc})
	if err == nil {
		t.Fatal("an undecodable answer gave no error")
	}
	v, _ := seen.Load("x")
	if n := v.(*atomic.Int32).Load(); n != 1 {
		t.Fatalf("the write was sent %d times, want 1", n)
	}
}
