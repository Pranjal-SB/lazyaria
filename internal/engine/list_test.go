package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

func TestListBuckets(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"a.bin": data, "p.bin": data, "x.bin": data})
	active := add(t, e, AddRequest{URIs: []string{fs.URL("a.bin", testutil.ModeSlow)}})
	paused := add(t, e, AddRequest{URIs: []string{fs.URL("p.bin", testutil.ModeOK)}, Paused: true})
	failed := add(t, e, AddRequest{URIs: []string{fs.URL("x.bin", testutil.Mode403)}})
	waitEvent(t, e, failed, KindError, 10*time.Second)
	waitStatus(t, e, active, 5*time.Second, func(s EngineStatus) bool { return s.State == "active" })

	list, err := e.List(ctxT(t, 5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	got := map[EngineRef]EngineStatus{}
	for _, s := range list {
		got[s.Ref] = s
	}
	want := map[EngineRef]string{active: "active", paused: "paused", failed: "error"}
	for ref, state := range want {
		if s := got[ref]; s.State != state || !s.Known || s.Name == "" {
			t.Fatalf("%s: want %s, got %+v", ref, state, s)
		}
	}
}

func TestReaddKeepsOptions(t *testing.T) {
	t.Parallel()
	e, _ := startEngine(t)
	fs := testutil.NewFaultServer(t, map[string][]byte{"o.bin": data})
	ref := add(t, e, AddRequest{
		URIs:    []string{fs.URL("o.bin", testutil.Mode403)},
		Options: map[string]string{"header": "X-Lazy: 1", "max-download-limit": "65536"},
		User:    "u", Pass: "p",
	})
	waitEvent(t, e, ref, KindError, 10*time.Second)
	got, err := e.ChangeURI(ctxT(t, 10*time.Second), ref, fs.URL("o.bin", testutil.ModeOK))
	if err != nil {
		t.Fatal(err)
	}
	var opt map[string]any
	rawCall(t, e, "aria2.getOption", &opt, string(got))
	hdr, _ := json.Marshal(opt["header"])
	if !bytes.Contains(hdr, []byte("X-Lazy: 1")) || opt["http-user"] != "u" || opt["http-passwd"] != "p" || opt["max-download-limit"] != "65536" {
		t.Fatalf("options lost on re-add: header=%s user=%v limit=%v", hdr, opt["http-user"], opt["max-download-limit"])
	}
}

func TestSupervisedRestartResync(t *testing.T) {
	t.Parallel()
	e, dir := startEngine(t)
	conf := filepath.Join(filepath.Dir(dir), "run", "aria2.conf")
	pid := aria2Pid(t, conf)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, e, "", KindResync, 15*time.Second)
	fs := testutil.NewFaultServer(t, map[string][]byte{"r.bin": data})
	ref := add(t, e, AddRequest{URIs: []string{fs.URL("r.bin", testutil.ModeOK)}})
	waitEvent(t, e, ref, KindComplete, 20*time.Second)
	if p := aria2Pid(t, conf); p == pid {
		t.Fatal("aria2c was not restarted")
	}
}

// aria2Pid finds the aria2c started with conf; each test has its own conf.
func aria2Pid(t *testing.T, conf string) int {
	t.Helper()
	ents, _ := os.ReadDir("/proc")
	for _, d := range ents {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + d.Name() + "/cmdline")
		if err == nil && strings.Contains(string(b), "--conf-path="+conf) {
			return pid
		}
	}
	t.Skip("cannot find the aria2c process (needs /proc)")
	return 0
}
