package engine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/supervisor"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

const fileSize = 2 << 20

var data = testutil.RandBytes(fileSize)

// startEngine runs a supervised engine on its own aria2c and temp dirs.
func startEngine(t *testing.T) (*aria2Engine, string) {
	t.Helper()
	testutil.RequireAria2(t)
	dir := t.TempDir()
	s, _, _, err := config.Load("", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.BT.EnableDHT = false
	p := config.Paths{
		StateDir:    filepath.Join(dir, "state"),
		RuntimeDir:  filepath.Join(dir, "run"),
		DownloadDir: filepath.Join(dir, "dl"),
	}
	p.AriaConf = filepath.Join(p.RuntimeDir, "aria2.conf")
	p.DHT = filepath.Join(p.StateDir, "dht.dat")
	for _, d := range []string{p.StateDir, p.RuntimeDir, p.DownloadDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sv := supervisor.New(s, p, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e := NewAria2(sv).(*aria2Engine)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := e.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		// Events must close, which means the follow goroutine has exited.
		for {
			select {
			case _, ok := <-e.Events():
				if !ok {
					return
				}
			case <-ctx.Done():
				t.Error("events channel not closed after Shutdown")
				return
			}
		}
	})
	return e, p.DownloadDir
}

func ctxT(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func add(t *testing.T, e Engine, req AddRequest) EngineRef {
	t.Helper()
	ref, err := e.Add(ctxT(t, 5*time.Second), req)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return ref
}

// waitEvent skips other events until ref gets kind.
func waitEvent(t *testing.T, e Engine, ref EngineRef, kind string, d time.Duration) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case ev, ok := <-e.Events():
			if !ok {
				t.Fatalf("events closed while waiting for %s", kind)
			}
			if ev.Ref == ref && ev.Kind == kind {
				return
			}
		case <-timeout:
			t.Fatalf("no %s event for %s within %v", kind, ref, d)
		}
	}
}

func status(t *testing.T, e Engine, ref EngineRef) EngineStatus {
	t.Helper()
	st, err := e.Status(ctxT(t, 5*time.Second), []EngineRef{ref})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return st[0]
}

// waitStatus polls until ok(status) holds.
func waitStatus(t *testing.T, e Engine, ref EngineRef, d time.Duration, ok func(EngineStatus) bool) EngineStatus {
	t.Helper()
	end := time.Now().Add(d)
	for {
		st := status(t, e, ref)
		if ok(st) {
			return st
		}
		if time.Now().After(end) {
			t.Fatalf("status of %s never matched; last %+v", ref, st)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func checkSHA(t *testing.T, path string) {
	t.Helper()
	want := testutil.SHA256File(t, writeTemp(t, data))
	if got := testutil.SHA256File(t, path); got != want {
		t.Fatalf("sha mismatch for %s", path)
	}
}

func writeTemp(t *testing.T, b []byte) string {
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func rawCall(t *testing.T, e *aria2Engine, method string, out any, params ...any) {
	t.Helper()
	c, err := e.client()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctxT(t, 5*time.Second), method, params, out); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}
