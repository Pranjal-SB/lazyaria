package ipc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestStalledClientDoesNotBlockShutdown(t *testing.T) {
	f := newFake()
	p := testPaths(shortDir(t))
	l, lock, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, f) }()

	raw, err := net.Dial("unix", p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close() // never read from: the client is stuck
	raw.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"jobs.subscribe","params":{"progress":true}}` + "\n"))
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		f.mu.Lock()
		n := len(f.observers)
		f.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subscribe never arrived")
		}
	}
	big := make([]daemon.ProgressItem, 2000)
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); time.Sleep(time.Millisecond) {
		f.bus.PublishProgress(big)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return with a stalled client connected")
	}
}

// Clients still sending while the daemon shuts down, as during an idle
// exit, must not crash it: jrpc2 panics when Stop races a request it is
// reading.
func TestShutdownWhileClientsSend(t *testing.T) {
	f := newFake()
	p := testPaths(shortDir(t))
	hello := []byte(`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"client":"t","version":"dev"}}` + "\n")
	for range 30 {
		l, lock, err := Listen(p)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Serve(ctx, l, f) }()
		var conns []net.Conn
		for range 20 {
			c, err := net.Dial("unix", p.Socket)
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, c)
			go io.Copy(io.Discard, c)
			go func() {
				for {
					if _, err := c.Write(hello); err != nil {
						return
					}
				}
			}()
		}
		time.Sleep(5 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Serve did not return")
		}
		for _, c := range conns {
			c.Close()
		}
		lock.Close()
	}
}

func TestPeerUIDSelf(t *testing.T) {
	p := testPaths(shortDir(t))
	l, lock, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	defer l.Close()
	go func() {
		if c, err := net.Dial("unix", p.Socket); err == nil {
			defer c.Close()
			io.Copy(io.Discard, c)
		}
	}()
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	uid, err := peerUID(c)
	if err != nil || uid != os.Geteuid() {
		t.Fatalf("peerUID = %d, %v; want %d", uid, err, os.Geteuid())
	}
}

func TestPeerUIDRejected(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs root on Linux to connect as another uid")
	}
	_, p := serve(t, newFake())
	// Open the socket up so only the peer check stands in the way.
	os.Chmod(filepath.Dir(p.Socket), 0o777)
	os.Chmod(p.StateDir, 0o777)
	os.Chmod(p.Socket, 0o777)
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "LAZYARIA_TEST_DIAL="+p.Socket)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	err := cmd.Run()
	if err != nil {
		t.Fatalf("connection from uid 65534 was not closed: %v", err)
	}
}

func TestRoundTripAddList(t *testing.T) {
	c, _ := serve(t, newFake())
	ctx := context.Background()
	j, err := c.Add(ctx, daemon.AddParams{URIs: []string{"https://example.com/a.iso"}})
	if err != nil {
		t.Fatal(err)
	}
	if j.ID != "j0" || j.URIs[0] != "https://example.com/a.iso" {
		t.Fatalf("Add returned %+v", j)
	}
	snap, err := c.List(ctx, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Seq != 7 || len(snap.Jobs) != 1 || snap.Jobs[0].ID != "j0" {
		t.Fatalf("List = %+v", snap)
	}
	tok, _, err := c.Remove(ctx, []string{"j0"}, false)
	if err != nil || tok != "tok" {
		t.Fatalf("Remove = %q, %v", tok, err)
	}
	e, err := c.ConfigGet(ctx, "downloads.dir")
	if err != nil || e.Source != "override" || e.Value != "/x" {
		t.Fatalf("ConfigGet = %+v, %v", e, err)
	}
	if _, err := c.ConfigGet(ctx, "nope"); err == nil {
		t.Fatal("ConfigGet of an unknown key succeeded")
	}
}

func TestPushEvents(t *testing.T) {
	f := newFake()
	c, _ := serve(t, f)
	ctx := context.Background()
	evs, prog, err := c.Subscribe(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"job.added", "job.updated", "job.removed"} {
		f.bus.Publish(daemon.Event{Type: typ, Job: &store.Job{ID: "a"}})
	}
	for i, want := range []string{"job.added", "job.updated", "job.removed"} {
		select {
		case ev := <-evs:
			if ev.Type != want || ev.Seq != uint64(i+1) || ev.Job.ID != "a" {
				t.Fatalf("event %d = %+v, want %s seq %d", i, ev, want, i+1)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
	f.bus.PublishProgress([]daemon.ProgressItem{{ID: "a", Done: 5}})
	select {
	case items := <-prog:
		if len(items) != 1 || items[0].Done != 5 {
			t.Fatalf("progress = %+v", items)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for progress")
	}
}

func TestObserversCounted(t *testing.T) {
	f := newFake()
	c, _ := serve(t, f)
	if _, _, err := c.Subscribe(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		got := append([]int(nil), f.observers...)
		f.mu.Unlock()
		if len(got) == 2 && got[0] == 1 && got[1] == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("SetObservers calls = %v, want [1 0]", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClientsCounted(t *testing.T) {
	f := newFake()
	c, _ := serve(t, f)
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		got := append([]int(nil), f.clients...)
		f.mu.Unlock()
		if len(got) == 2 && got[0] == 1 && got[1] == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("SetClients calls = %v, want [1 0]", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDuplicateErrorMapsToCode(t *testing.T) {
	f := newFake()
	f.addErr = &daemon.DuplicateError{Existing: &store.Job{ID: "old", Name: "a.iso"}}
	c, _ := serve(t, f)
	_, err := c.Add(context.Background(), daemon.AddParams{URIs: []string{"https://example.com/a.iso"}})
	var dup *daemon.DuplicateError
	if !errors.As(err, &dup) || dup.Existing == nil || dup.Existing.ID != "old" {
		t.Fatalf("Add error = %#v, want *daemon.DuplicateError with Existing", err)
	}
}

func TestErrorMapping(t *testing.T) {
	ctx := context.Background()
	add := func(err error) (*store.Job, error) {
		f := newFake()
		f.addErr = err
		c, _ := serve(t, f)
		return c.Add(ctx, daemon.AddParams{URIs: []string{"https://example.com/a"}})
	}

	j, err := add(&daemon.SkippedError{Existing: &store.Job{ID: "old"}})
	var sk *daemon.SkippedError
	if !errors.As(err, &sk) || j == nil || j.ID != "old" {
		t.Errorf("skipped: job %v, err %#v", j, err)
	}

	_, err = add(&daemon.NoSpaceError{Dir: "/d", Need: 10, Free: 1})
	var ns *daemon.NoSpaceError
	if !errors.As(err, &ns) || ns.Dir != "/d" || ns.Need != 10 || ns.Free != 1 {
		t.Errorf("no space: %#v", err)
	}

	_, err = add(engine.ErrInvalidOption{Name: "on-download-complete"})
	var inv engine.ErrInvalidOption
	if !errors.As(err, &inv) || inv.Name != "on-download-complete" {
		t.Errorf("invalid option: %#v", err)
	}
	_, err = add(engine.ErrInvalidOption{Name: "out", Reason: "drop --out"})
	if !errors.As(err, &inv) || inv.Reason != "drop --out" {
		t.Errorf("invalid option reason: %#v", err)
	}
}

func TestRedactsCredentials(t *testing.T) {
	f := newFake()
	c, _ := serve(t, f)
	ctx := context.Background()
	evs, _, err := c.Subscribe(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	secret := map[string]string{"http-passwd": "hunter2", "ftp-passwd": "hunter3", "header": "Authorization: Bearer xyz", "split": "4"}
	added, err := c.Add(ctx, daemon.AddParams{URIs: []string{"https://example.com/a"}, Options: secret})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := c.List(ctx, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	orig := f.jobs[0]
	f.mu.Unlock()
	f.bus.Publish(daemon.Event{Type: "job.added", Job: orig})
	ev := <-evs
	for name, j := range map[string]*store.Job{"add reply": added, "snapshot": snap.Jobs[0], "event": ev.Job} {
		for _, k := range []string{"http-passwd", "ftp-passwd", "header"} {
			if v, ok := j.Options[k]; ok && v != "***" {
				t.Errorf("%s leaks %s = %q", name, k, v)
			}
		}
		if j.Options["split"] != "4" {
			t.Errorf("%s lost a harmless option: %v", name, j.Options)
		}
	}
	if orig.Options["http-passwd"] != "hunter2" || orig.Options["header"] != "Authorization: Bearer xyz" {
		t.Errorf("redaction mutated the shared job: %v", orig.Options)
	}
}

func TestFrameTooLarge(t *testing.T) {
	big := bytes.Repeat([]byte("x"), MaxFrame+1)
	r := &capReader{r: io.MultiReader(strings.NewReader("{}\n"), bytes.NewReader(big))}
	_, err := io.ReadAll(r)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err = %v, want ErrFrameTooLarge", err)
	}
	ok := &capReader{r: strings.NewReader(strings.Repeat("y", 100) + "\n" + strings.Repeat("z", 100) + "\n")}
	if _, err := io.ReadAll(ok); err != nil {
		t.Fatalf("small frames: %v", err)
	}
}
