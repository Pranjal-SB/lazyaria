//go:build e2e && linux

package e2e

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/testutil"
)

// TestNoNetworkListeners is quality bar Q8, checked while a download runs:
// lazyaria listens on no TCP port, aria2 only on loopback, the RPC secret
// is not in anyone's argv, and the private files are 0600.
func TestNoNetworkListeners(t *testing.T) {
	t.Parallel()
	srv := testutil.NewFaultServer(t, map[string][]byte{"sec.bin": payload(1<<20, 0x31)})
	e := newEnv(t, "")
	ids := e.add(srv.URL("sec.bin", testutil.ModeSlow))
	if len(ids) != 1 {
		t.Fatalf("add returned %v", ids)
	}
	e.waitState(ids[0], 15*time.Second, store.Active)
	pid := e.status().PID
	ariaPID := aria2Child(pid)
	if ariaPID == 0 {
		t.Fatal("no aria2c child during a download")
	}

	listeners := tcpListeners(t)
	if ls := owned(t, pid, listeners); len(ls) > 0 {
		t.Errorf("the daemon (pid %d) listens on %v", pid, ls)
	}
	ariaLs := owned(t, ariaPID, listeners)
	if len(ariaLs) == 0 {
		t.Error("aria2c has no listener at all; the RPC port should show up, so the scan is broken")
	}
	t.Logf("aria2c (pid %d) listens on %v", ariaPID, ariaLs)
	for _, a := range ariaLs {
		if !a.IP.IsLoopback() {
			t.Errorf("aria2c listens on %s, which is not loopback", a)
		}
	}

	secret := confValue(t, e.p.AriaConf, "rpc-secret")
	if len(secret) != 32 {
		t.Errorf("rpc-secret is %d chars, want 32", len(secret))
	}
	if v := confValue(t, e.p.AriaConf, "rpc-listen-all"); v != "false" {
		t.Errorf("rpc-listen-all=%q, want false", v)
	}
	for _, p := range []int{pid, ariaPID} {
		argv, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", p))
		if err != nil {
			t.Fatal(err)
		}
		if secret != "" && bytes.Contains(argv, []byte(secret)) {
			t.Errorf("pid %d has the RPC secret in its argv", p)
		}
	}

	for path, want := range map[string]os.FileMode{
		e.p.Socket:     0o600,
		e.p.DB:         0o600,
		e.p.AriaConf:   0o600,
		e.p.RuntimeDir: 0o700,
		e.p.StateDir:   0o700,
	} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Errorf("stat %s: %v", path, err)
			continue
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s is %o, want %o", filepath.Base(path), got, want)
		}
	}
}

// tcpListeners maps socket inode to address for every listening TCP socket.
func tcpListeners(t *testing.T) map[string]*net.TCPAddr {
	t.Helper()
	ls := map[string]*net.TCPAddr{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode
			fs := strings.Fields(line)
			if len(fs) < 10 || fs[3] != "0A" {
				continue
			}
			ls[fs[9]] = procAddr(t, fs[1])
		}
	}
	return ls
}

// procAddr decodes "0100007F:1F90": the IP is in host order per 32-bit word.
func procAddr(t *testing.T, s string) *net.TCPAddr {
	t.Helper()
	hexIP, hexPort, _ := strings.Cut(s, ":")
	raw, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		t.Fatalf("bad /proc/net address %q", s)
	}
	ip := make(net.IP, len(hexIP)/2)
	for i := 0; i < len(ip); i += 4 {
		w, err := strconv.ParseUint(hexIP[i*2:i*2+8], 16, 32)
		if err != nil {
			t.Fatalf("bad /proc/net address %q", s)
		}
		ip[i], ip[i+1], ip[i+2], ip[i+3] = byte(w), byte(w>>8), byte(w>>16), byte(w>>24)
	}
	return &net.TCPAddr{IP: ip, Port: int(raw)}
}

// owned returns the listeners whose socket is open in pid.
func owned(t *testing.T, pid int, ls map[string]*net.TCPAddr) []*net.TCPAddr {
	t.Helper()
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	fds, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []*net.TCPAddr
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(dir, fd.Name()))
		inode, ok := strings.CutPrefix(link, "socket:[")
		if err != nil || !ok {
			continue
		}
		if a := ls[strings.TrimSuffix(inode, "]")]; a != nil {
			out = append(out, a)
		}
	}
	return out
}

func confValue(t *testing.T, path, key string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), key+"="); ok {
			return v
		}
	}
	return ""
}
