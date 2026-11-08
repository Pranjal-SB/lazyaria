package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/config"
)

func testSettings(t *testing.T) (*config.Settings, config.Paths) {
	t.Helper()
	return settingsIn(t, t.TempDir())
}

func settingsIn(t *testing.T, dir string) (*config.Settings, config.Paths) {
	t.Helper()
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
	return s, p
}

func confLines(t *testing.T, path string) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string][]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			t.Fatalf("malformed conf line %q", l)
		}
		m[k] = append(m[k], v)
	}
	return m
}

func TestWriteConf(t *testing.T) {
	s, p := testSettings(t)
	s.BT.SeedRatio = 1.5
	s.BT.SeedMinutes = 0
	s.Engine.Aria2ExtraArgs = []string{"--max-tries=3", "--check-integrity"}
	secret := "0123456789abcdef0123456789abcdef"
	if err := WriteConf(p.AriaConf, s, p, 41234, secret); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p.AriaConf)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
	m := confLines(t, p.AriaConf)
	want := map[string]string{
		"enable-rpc":         "true",
		"rpc-listen-all":     "false",
		"rpc-listen-port":    "41234",
		"rpc-secret":         secret,
		"seed-time":          "0",
		"seed-ratio":         "1.5",
		"dir":                p.DownloadDir,
		"log":                filepath.Join(p.StateDir, "aria2.log"),
		"dht-file-path":      p.DHT,
		"daemon":             "false",
		"disk-cache":         "67108864",
		"auto-save-interval": "10",
		"retry-wait":         "10",
		"check-integrity":    "true",
	}
	for k, v := range want {
		if got := m[k]; len(got) == 0 || got[len(got)-1] != v {
			t.Errorf("%s = %v, want %q last", k, got, v)
		}
	}
	if got := m["file-allocation"]; len(got) != 1 || got[0] == "" {
		t.Errorf("file-allocation = %v", got)
	}
	// extra args come last, so they win over our max-tries
	if got := m["max-tries"]; got[len(got)-1] != "3" {
		t.Errorf("max-tries = %v, want extra arg last", got)
	}
}

func TestWriteConfSeedTime(t *testing.T) {
	s, p := testSettings(t)
	s.BT.SeedMinutes = 90
	if err := WriteConf(p.AriaConf, s, p, 1, "s"); err != nil {
		t.Fatal(err)
	}
	if got := confLines(t, p.AriaConf)["seed-time"]; got[0] != "90" {
		t.Fatalf("seed-time = %v", got)
	}
}

func TestWriteConfRejectsNewlines(t *testing.T) {
	cases := map[string]func(*config.Settings){
		"extra arg":  func(s *config.Settings) { s.Engine.Aria2ExtraArgs = []string{"--dir=/x\nrpc-listen-all=true"} },
		"user agent": func(s *config.Settings) { s.Downloads.UserAgent = "ua\rrpc-listen-all=true" },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			s, p := testSettings(t)
			mod(s)
			if err := WriteConf(p.AriaConf, s, p, 1, "s"); err == nil {
				t.Fatal("want error for a value with a line break")
			}
		})
	}
}

// aria2 trims whitespace around conf keys, so " rpc-listen-all=true" would
// open the RPC port to the network.
func TestWriteConfRejectsReservedExtraArgs(t *testing.T) {
	for _, a := range []string{"-x16", "--rpc-listen-all=true", "--enable-rpc=false", "-- rpc-listen-all=true",
		"--\trpc-secret=x", " --rpc-secret=x", "--log-level=debug", "--conf-path=/x", "--log=/x",
		"--stop-with-process=1", "--RPC-SECRET=x", "---rpc-secret=x", "--daemon=true", "--input-file=/x",
		"--save-session=/x"} {
		s, p := testSettings(t)
		s.Engine.Aria2ExtraArgs = []string{a}
		if err := WriteConf(p.AriaConf, s, p, 1, "s"); err == nil {
			t.Errorf("%q accepted", a)
		}
	}
}

func TestWriteConfTightensMode(t *testing.T) {
	s, p := testSettings(t)
	if err := os.WriteFile(p.AriaConf, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteConf(p.AriaConf, s, p, 1, "s"); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p.AriaConf)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
}
