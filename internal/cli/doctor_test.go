package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/store"
)

type checkRow struct {
	Check  string `json:"check"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix"`
}

func doctorJSON(t *testing.T, args ...string) (int, map[string]checkRow) {
	t.Helper()
	code, out, errOut := run(t, Doctor, "", append([]string{"--json"}, args...)...)
	var rows []checkRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("doctor --json: %v\n%s\n%s", err, out, errOut)
	}
	m := map[string]checkRow{}
	for _, r := range rows {
		m[r.Check] = r
	}
	return code, m
}

func TestDoctorChecks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("os-release is linux only")
	}
	isolate(t)
	osr := filepath.Join(t.TempDir(), "os-release")
	os.WriteFile(osr, []byte("NAME=\"Arch Linux\"\nID=arch\n"), 0o600)
	old := osReleasePath
	osReleasePath = osr
	t.Cleanup(func() { osReleasePath = old })
	t.Setenv("PATH", "")
	t.Setenv("https_proxy", "http://user:secret@proxy:3128")

	code, m := doctorJSON(t)
	if code != 1 {
		t.Errorf("exit %d, want 1 with a failing check", code)
	}
	a, ok := m["aria2c"]
	if !ok || a.OK || !strings.Contains(a.Fix, "sudo pacman -S aria2") {
		t.Errorf("aria2c row = %+v", a)
	}
	for _, name := range []string{"runtime dir", "socket", "daemon", "database", "download dir", "xattr", "logind", "terminal", "proxy", "tls", "yt-dlp", "ffmpeg", "notify-send", "wl-paste"} {
		if _, ok := m[name]; !ok {
			t.Errorf("missing check %q", name)
		}
	}
	p := m["proxy"]
	if !strings.Contains(p.Detail, "https_proxy") || strings.Contains(p.Detail, "secret") {
		t.Errorf("proxy row should name the var and hide the password: %+v", p)
	}
	if !m["yt-dlp"].OK {
		t.Error("optional tools never fail the doctor")
	}
}

func TestDoctorBTPortsAndRestart(t *testing.T) {
	f := startFake(t)
	f.jobs = []*store.Job{{ID: "01JC0000000000000000000009", Kind: "magnet", State: store.Active}}
	_, m := doctorJSON(t, "--restart-engine")
	bt, ok := m["bt ports"]
	if !ok || !strings.Contains(bt.Detail, "6881-6999") {
		t.Errorf("bt ports row = %+v", bt)
	}
	if !m["daemon"].OK {
		t.Errorf("daemon row = %+v", m["daemon"])
	}
	if f.restarts != 1 {
		t.Errorf("engine restarts = %d, want 1", f.restarts)
	}
	tls := m["tls"]
	if !tls.OK || !strings.Contains(tls.Detail, "restart") {
		t.Errorf("tls row = %+v", tls)
	}
}

func TestDoctorNoBTPortsWithoutTorrents(t *testing.T) {
	startFake(t)
	if _, m := doctorJSON(t); m["bt ports"].Check != "" {
		t.Errorf("bt ports shown with no torrents: %+v", m["bt ports"])
	}
}
