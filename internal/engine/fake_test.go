package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
	"github.com/coder/websocket"
)

// fakeAria2 answers JSON-RPC with whatever reply returns: a fragment such
// as `"result":"OK"` or `"error":{...}`, or "" to never answer.
type fakeAria2 struct {
	mu    sync.Mutex
	calls []string
	args  [][]json.RawMessage
}

func startFake(t *testing.T, reply func(method string, n int) string) (*fakeAria2, Engine) {
	t.Helper()
	f := &fakeAria2{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var req struct {
				ID     string
				Method string
				Params []json.RawMessage
			}
			json.Unmarshal(b, &req)
			f.mu.Lock()
			f.calls = append(f.calls, req.Method)
			f.args = append(f.args, req.Params)
			n := f.countLocked(req.Method)
			f.mu.Unlock()
			if frag := reply(req.Method, n); frag != "" {
				c.Write(r.Context(), websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":"`+req.ID+`",`+frag+`}`))
			}
		}
	}))
	t.Cleanup(srv.Close)
	e := NewAria2Attach("ws"+strings.TrimPrefix(srv.URL, "http")+"/jsonrpc", "")
	if err := e.Start(ctxT(t, 5*time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Shutdown(context.Background()) })
	return f, e
}

func (f *fakeAria2) countLocked(method string) int {
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

func (f *fakeAria2) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.countLocked(method)
}

// paramsOf returns the params of the nth (1-based) call to method.
func (f *fakeAria2) paramsOf(method string, n int) []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.calls {
		if c == method {
			if n--; n == 0 {
				return f.args[i]
			}
		}
	}
	return nil
}

func TestChangeURICompletesDuringPause(t *testing.T) {
	f, e := startFake(t, func(m string, n int) string {
		switch {
		case m == "aria2.tellStatus" && n == 1:
			return `"result":{"status":"active","dir":"/d","files":[]}`
		case m == "aria2.tellStatus":
			return `"result":{"status":"complete"}`
		}
		return `"result":"OK"`
	})
	start := time.Now()
	_, err := e.ChangeURI(context.Background(), "abc", "http://h/f")
	if !errors.Is(err, ErrAlreadyComplete) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if f.count("aria2.unpause") != 0 || f.count("aria2.changeUri") != 0 {
		t.Fatal("touched a finished download")
	}
}

func TestChangeURIUnpausesAfterWaitTimeout(t *testing.T) {
	f, e := startFake(t, func(m string, n int) string {
		if m == "aria2.tellStatus" {
			return `"result":{"status":"active","dir":"/d","files":[]}` // the pause never lands
		}
		return `"result":"OK"`
	})
	if _, err := e.ChangeURI(ctxT(t, 500*time.Millisecond), "abc", "http://h/f"); err == nil {
		t.Fatal("want a timeout")
	}
	if f.count("aria2.unpause") != 1 {
		t.Fatal("a download we paused was left paused")
	}
}

func readdFake(t *testing.T, add func(n int) string) (*fakeAria2, Engine) {
	return startFake(t, func(m string, n int) string {
		switch m {
		case "aria2.tellStatus":
			return `"result":{"status":"error","dir":"/d","files":[{"path":"/d/f","uris":[]}]}`
		case "aria2.getOption", "aria2.getGlobalOption":
			return `"result":{}`
		case "aria2.addUri":
			return add(n)
		}
		return `"result":"OK"`
	})
}

// Only an aria2 refusal may trigger the new-gid fallback: after a lost or
// garbled reply the first add may have gone through.
func TestReaddNoSecondAddUnlessRefused(t *testing.T) {
	for name, reply := range map[string]string{"timeout": "", "garbled": `"result":123`} {
		t.Run(name, func(t *testing.T) {
			f, e := readdFake(t, func(int) string { return reply })
			if _, err := e.ChangeURI(ctxT(t, time.Second), "abc", "http://h/f"); err == nil {
				t.Fatal("want an error")
			}
			if n := f.count("aria2.addUri"); n != 1 {
				t.Fatalf("addUri sent %d times", n)
			}
		})
	}
}

func TestReaddFallsBackOnRefusal(t *testing.T) {
	f, e := readdFake(t, func(n int) string {
		if n == 1 {
			return `"error":{"code":1,"message":"GID abc is not unique"}`
		}
		return `"result":"def"`
	})
	ref, err := e.ChangeURI(ctxT(t, 5*time.Second), "abc", "http://h/f")
	if err != nil || ref != "def" {
		t.Fatalf("ref %q err %v", ref, err)
	}
	var opts map[string]string
	json.Unmarshal(f.paramsOf("aria2.addUri", 2)[1], &opts)
	if _, has := opts["gid"]; has || opts["out"] != "f" || opts["dir"] != "/d" {
		t.Fatalf("fallback options %v", opts)
	}
}

func TestChangeURIOnTorrent(t *testing.T) {
	_, e := startFake(t, func(string, int) string {
		return `"result":{"status":"active","bittorrent":{"info":{"name":"x"}}}`
	})
	if _, err := e.ChangeURI(ctxT(t, 5*time.Second), "abc", "http://h/f"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("got %v", err)
	}
}

func TestAddRejectsUnparseableURI(t *testing.T) {
	e := NewAria2Attach("ws://127.0.0.1:1/jsonrpc", "")
	_, err := e.Add(ctxT(t, time.Second), AddRequest{URIs: []string{"http://u:secret@h/%zz"}})
	if err == nil || errors.Is(err, ErrNotRunning) || !strings.Contains(err.Error(), "not a valid URL") {
		t.Fatalf("want the URL check to reject it, got %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("credentials in error: %v", err)
	}
}

func TestEmitCoalescesWhenFull(t *testing.T) {
	e := newAria2(nil, "", "")
	for range cap(e.events) {
		e.emit(EngineEvent{Ref: "a", Kind: KindStart})
	}
	e.emit(EngineEvent{Ref: "b", Kind: KindComplete}) // must not block
	e.emit(EngineEvent{Ref: "c", Kind: KindComplete})
	if !e.pendingResync {
		t.Fatal("dropped events must leave a resync pending")
	}
	<-e.events
	<-e.events
	e.emit(EngineEvent{Ref: "d", Kind: KindStart})
	var tail []EngineEvent
	for len(e.events) > 0 {
		tail = append(tail, <-e.events)
	}
	n := len(tail)
	if tail[n-2].Kind != KindResync || tail[n-1].Ref != "d" || e.pendingResync {
		t.Fatalf("want one resync then d once there is room, got %+v %+v", tail[n-2], tail[n-1])
	}
}

func TestStartAfterShutdown(t *testing.T) {
	e := NewAria2Attach("ws://127.0.0.1:1/jsonrpc", "")
	e.Shutdown(context.Background())
	if err := e.Start(ctxT(t, time.Second)); err == nil {
		t.Fatal("Start after Shutdown must fail")
	}
}

func TestNameFallback(t *testing.T) {
	for _, u := range []string{"http://h/", "http://h"} {
		var raw aria2.Status
		in := `{"gid":"g","status":"error","files":[{"path":"","uris":[{"uri":"` + u + `"}]}]}`
		if err := json.Unmarshal([]byte(in), &raw); err != nil {
			t.Fatal(err)
		}
		if got := toStatus(&raw).Name; got != "download" {
			t.Fatalf("%s: name %q", u, got)
		}
	}
}
