package ipc

import (
	"bytes"
	"context"
	"io"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// heldSnapshot answers jobs.snapshot only once the connection ends.
type heldSnapshot struct {
	*fakeHandler
	waiting atomic.Int32
}

func (h *heldSnapshot) Snapshot(ctx context.Context, f store.Filter) daemon.Snapshot {
	h.waiting.Add(1)
	<-ctx.Done()
	return h.fakeHandler.Snapshot(ctx, f)
}

// Calls running at once on one client all survive the daemon going away:
// one reconnect is made, and each read-only call is sent again on it.
func TestConcurrentCallsAcrossReconnect(t *testing.T) {
	const n = 8
	_, pB := serve(t, newFake())

	h := &heldSnapshot{fakeHandler: newFake()}
	pA := testPaths(shortDir(t))
	l, lock, err := Listen(pA)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, stopA := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, h) }()

	pR, sent := relay(t, pA.Socket)
	c, err := Dial(context.Background(), pR)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var redials atomic.Int32
	c.Redial = func(ctx context.Context) (*Client, error) {
		redials.Add(1)
		return Dial(ctx, pB)
	}

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for range n {
		wg.Go(func() {
			_, err := c.List(context.Background(), false, 0)
			errs <- err
		})
	}
	// The daemon runs up to maxCallsPerConn handlers per connection, so
	// wait until every call is on the wire and held there.
	held := int32(min(n, maxCallsPerConn))
	for deadline := time.Now().Add(30 * time.Second); sent.Load() < n || h.waiting.Load() < held; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d calls sent, %d of %d held by the daemon", sent.Load(), n, h.waiting.Load(), held)
		}
	}
	stopA()
	<-done
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("call across the reconnect: %v", err)
		}
	}
	if got := redials.Load(); got != 1 {
		t.Errorf("redialled %d times, want 1", got)
	}
	if _, err := c.List(context.Background(), false, 0); err != nil {
		t.Errorf("call after the reconnect: %v", err)
	}
}

// relay serves a socket that forwards each connection to sock and counts
// the lines clients have written through to sock. When either side ends,
// both are closed, so a hang-up reaches the client as it would directly.
func relay(t *testing.T, sock string) (config.Paths, *atomic.Int32) {
	t.Helper()
	p := config.Paths{Socket: filepath.Join(shortDir(t), "r.sock")}
	l, err := net.Listen("unix", p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	lines := new(atomic.Int32)
	go func() {
		for {
			down, err := l.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("unix", sock)
			if err != nil {
				down.Close()
				continue
			}
			end := sync.OnceFunc(func() { down.Close(); up.Close() })
			go func() { io.Copy(lineCounter{up, lines}, down); end() }()
			go func() { io.Copy(down, up); end() }()
		}
	}()
	return p, lines
}

type lineCounter struct {
	io.Writer
	n *atomic.Int32
}

func (w lineCounter) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	w.n.Add(int32(bytes.Count(b[:n], []byte("\n"))))
	return n, err
}
