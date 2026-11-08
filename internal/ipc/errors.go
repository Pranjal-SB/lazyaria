package ipc

import (
	"encoding/json"
	"errors"
	"maps"

	"github.com/creachadair/jrpc2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

// Error codes for daemon errors that cross the socket with their data.
const (
	CodeDuplicate jrpc2.Code = -32001
	CodeSkipped   jrpc2.Code = -32002
	CodeNoSpace   jrpc2.Code = -32003
)

// toWire turns a handler error into a JSON-RPC error carrying enough data
// for the client to rebuild the typed error.
func toWire(err error) error {
	if err == nil {
		return nil
	}
	var (
		dup *daemon.DuplicateError
		sk  *daemon.SkippedError
		ns  *daemon.NoSpaceError
		inv engine.ErrInvalidOption
	)
	switch {
	case errors.As(err, &dup):
		return jrpc2.Errorf(CodeDuplicate, "%s", err).WithData(&daemon.DuplicateError{Existing: redact(dup.Existing), Path: dup.Path})
	case errors.As(err, &sk):
		return jrpc2.Errorf(CodeSkipped, "%s", err).WithData(&daemon.SkippedError{Existing: redact(sk.Existing), Path: sk.Path})
	case errors.As(err, &ns):
		return jrpc2.Errorf(CodeNoSpace, "%s", err).WithData(ns)
	case errors.As(err, &inv):
		return jrpc2.Errorf(jrpc2.InvalidParams, "%s", err).WithData(inv)
	}
	return err
}

// fromWire is the client half of toWire.
func fromWire(err error) error {
	var e *jrpc2.Error
	if !errors.As(err, &e) {
		return err
	}
	var out error
	switch e.Code {
	case CodeDuplicate:
		out = decode(e, &daemon.DuplicateError{})
	case CodeSkipped:
		out = decode(e, &daemon.SkippedError{})
	case CodeNoSpace:
		out = decode(e, &daemon.NoSpaceError{})
	case jrpc2.InvalidParams:
		var inv engine.ErrInvalidOption
		if json.Unmarshal(e.Data, &inv) == nil && inv.Name != "" {
			out = inv
		}
	}
	if out == nil {
		return err
	}
	return out
}

func decode[T error](e *jrpc2.Error, v T) error {
	if json.Unmarshal(e.Data, v) != nil {
		return nil
	}
	return v
}

// secretOptions are job options whose values must never leave the daemon;
// headers can carry Authorization or Cookie.
var secretOptions = []string{"http-passwd", "ftp-passwd", "header"}

// redact returns j with secret option values masked. j is shared with the
// bus and the manager, so it is copied rather than changed.
func redact(j *store.Job) *store.Job {
	if j == nil {
		return nil
	}
	c := *j
	c.Options = maps.Clone(j.Options)
	for _, k := range secretOptions {
		if _, ok := c.Options[k]; ok {
			c.Options[k] = "***"
		}
	}
	return &c
}

func redactAll(js []*store.Job) []*store.Job {
	out := make([]*store.Job, len(js))
	for i, j := range js {
		out[i] = redact(j)
	}
	return out
}
