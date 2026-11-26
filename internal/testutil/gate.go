package testutil

import (
	"bytes"
	"context"
	"time"
)

// SetGate makes the "ok" and "slow" modes serve name only up to file
// offset upTo: a reply holds there until the gate is raised. A test drives
// a download to a chosen point this way. A negative upTo opens it.
func (f *FaultServer) SetGate(name string, upTo int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if upTo < 0 {
		delete(f.gates, name)
		return
	}
	f.gates[name] = upTo
}

func (f *FaultServer) gate(name string) (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.gates[name]
	return g, ok
}

// gated reads a file, waiting at the offset its gate allows until the gate
// rises or the request ends.
type gated struct {
	*bytes.Reader
	ctx   context.Context
	f     *FaultServer
	name  string
	flush func() // sends what is buffered, before a wait
}

func (g *gated) Read(p []byte) (int, error) {
	for flushed := false; ; flushed = true {
		pos := g.Size() - int64(g.Len())
		upTo, ok := g.f.gate(g.name)
		if !ok || g.Len() == 0 {
			return g.Reader.Read(p)
		}
		if upTo > pos {
			return g.Reader.Read(p[:min(int64(len(p)), upTo-pos)])
		}
		if !flushed {
			g.flush()
		}
		select {
		case <-g.ctx.Done():
			return 0, g.ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
