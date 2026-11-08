// Package testutil holds helpers for the integration tests: a fault-injecting
// HTTP file server, aria2 discovery and file hashing.
package testutil

import (
	"bytes"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Mode selects how the fault server answers a request.
type Mode string

const (
	ModeOK      Mode = "ok"
	Mode403     Mode = "403"
	Mode404     Mode = "404"
	Mode410     Mode = "410"
	Mode429     Mode = "429"
	Mode503     Mode = "503"
	ModeSlow    Mode = "slow"
	ModeExpire  Mode = "expire"
	ModeDrop    Mode = "drop"
	ModeNoRange Mode = "norange"
)

const (
	expireLimit = 256 << 10 // bytes served per name before "expire" starts returning 403
	dropAfter   = 128 << 10 // bytes sent before "drop" closes the connection
	slowRate    = 128 << 10 // bytes per second for "slow"
	slowChunk   = 4 << 10
)

// FaultServer serves named files and fails them on demand.
type FaultServer struct {
	srv       *httptest.Server
	files     map[string][]byte
	mu        sync.Mutex
	hits      map[string]int
	served    map[string]int64
	overrides map[string]Mode
}

// NewFaultServer starts a server for files. The server is closed when t ends.
func NewFaultServer(t *testing.T, files map[string][]byte) *FaultServer {
	t.Helper()
	f := &FaultServer{
		files:     files,
		hits:      make(map[string]int),
		served:    make(map[string]int64),
		overrides: make(map[string]Mode),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// URL returns the address of name fetched in mode, shaped as <server>/<mode>/<name>.
func (f *FaultServer) URL(name string, mode Mode) string {
	return f.srv.URL + "/" + string(mode) + "/" + url.PathEscape(name)
}

// SetMode overrides the mode for name, whatever the path says.
func (f *FaultServer) SetMode(name string, m Mode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides[name] = m
}

// Hits returns how many requests name has received, in any mode.
func (f *FaultServer) Hits(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[name]
}

func (f *FaultServer) serve(w http.ResponseWriter, r *http.Request) {
	mode, name, ok := splitPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	f.mu.Lock()
	f.hits[name]++
	if m, set := f.overrides[name]; set {
		mode = m
	}
	served := f.served[name]
	f.mu.Unlock()

	data, found := f.files[name]
	if !found {
		http.NotFound(w, r)
		return
	}

	cw := &countWriter{ResponseWriter: w, f: f, name: name}
	switch mode {
	case ModeOK:
		http.ServeContent(cw, r, name, time.Time{}, bytes.NewReader(data))
	case Mode403, Mode404, Mode410, Mode503:
		code, _ := strconv.Atoi(string(mode))
		http.Error(w, http.StatusText(code), code)
	case Mode429:
		w.Header().Set("Retry-After", "1")
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
	case ModeSlow:
		th := &throttled{ResponseWriter: cw, start: time.Now()}
		http.ServeContent(th, r, name, time.Time{}, bytes.NewReader(data))
	case ModeExpire:
		if served >= expireLimit {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		http.ServeContent(cw, r, name, time.Time{}, bytes.NewReader(data))
	case ModeDrop:
		serveDrop(w, cw, data)
	case ModeNoRange:
		serveNoRange(cw, data)
	default:
		http.Error(w, "unknown fault mode", http.StatusBadRequest)
	}
}

// splitPath turns "/<mode>/<name>" into its two parts.
func splitPath(p string) (Mode, string, bool) {
	parts := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", false
	}
	return Mode(parts[0]), parts[1], true
}

// serveDrop sends the first dropAfter bytes and then cuts the TCP connection.
func serveDrop(w http.ResponseWriter, cw *countWriter, data []byte) {
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	n := min(len(data), dropAfter)
	_, _ = cw.Write(data[:n])

	rc := http.NewResponseController(w)
	_ = rc.Flush()
	conn, _, err := rc.Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

// serveNoRange ignores Range and streams the whole file with chunked encoding.
func serveNoRange(cw *countWriter, data []byte) {
	cw.Header().Set("Content-Type", "application/octet-stream")
	cw.WriteHeader(http.StatusOK)

	first := min(len(data), slowChunk)
	_, _ = cw.Write(data[:first])
	_ = http.NewResponseController(cw).Flush()
	_, _ = cw.Write(data[first:])
}

// countWriter adds every body byte to the per-name served counter before it
// goes out. Counting first means the client never sees bytes the counter
// has not yet recorded.
type countWriter struct {
	http.ResponseWriter
	f    *FaultServer
	name string
}

func (c *countWriter) Write(p []byte) (int, error) {
	c.f.mu.Lock()
	c.f.served[c.name] += int64(len(p))
	c.f.mu.Unlock()
	return c.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (c *countWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// throttled paces writes so the total never runs ahead of slowRate.
type throttled struct {
	http.ResponseWriter
	start time.Time
	sent  int64
}

func (t *throttled) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := min(len(p), slowChunk)
		m, err := t.ResponseWriter.Write(p[:n])
		total += m
		t.sent += int64(m)
		_ = http.NewResponseController(t.ResponseWriter).Flush()
		if wait := time.Until(t.start.Add(time.Duration(t.sent) * time.Second / slowRate)); wait > 0 {
			time.Sleep(wait)
		}
		if err != nil {
			return total, err
		}
		p = p[n:]
	}
	return total, nil
}

var randSeed = [32]byte{'l', 'a', 'z', 'y', 'a', 'r', 'i', 'a', 0x01}

// RandBytes returns n bytes that are the same on every call.
func RandBytes(n int) []byte {
	buf := make([]byte, n)
	c := rand.NewChaCha8(randSeed)
	_, _ = c.Read(buf)
	return buf
}

// Served returns how many body bytes name has sent, in any mode.
func (f *FaultServer) Served(name string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.served[name]
}
