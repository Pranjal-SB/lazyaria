package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestRuntimeDirXDG(t *testing.T) {
	env := envMap(map[string]string{"HOME": "/home/u", "XDG_RUNTIME_DIR": "/run/user/1000"})
	p, err := resolvePaths(&Settings{}, env, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.RuntimeDir != "/run/user/1000/lazyaria" || p.Socket != p.RuntimeDir+"/lazyaria.sock" {
		t.Fatalf("got %q %q", p.RuntimeDir, p.Socket)
	}
	if p.ConfigFile != "/home/u/.config/lazyaria/config.toml" || p.StateDir != "/home/u/.local/state/lazyaria" {
		t.Fatalf("got %q %q", p.ConfigFile, p.StateDir)
	}
	if p.DB != p.StateDir+"/lazyaria.db" || p.Log != p.StateDir+"/lazyaria.log" || p.DHT != p.StateDir+"/dht.dat" {
		t.Fatalf("state files wrong: %+v", p)
	}
	if p.Lock != p.RuntimeDir+"/daemon.lock" || p.AriaConf != p.RuntimeDir+"/aria2.conf" {
		t.Fatalf("runtime files wrong: %+v", p)
	}
}

func TestRuntimeDirFallbackState(t *testing.T) {
	env := envMap(map[string]string{"HOME": "/home/u"})
	p, err := resolvePaths(&Settings{}, env, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Socket, p.StateDir+"/run/") {
		t.Fatalf("socket %q not under state/run", p.Socket)
	}
}

func TestSocketPathTooLong(t *testing.T) {
	tmp := t.TempDir()
	long := "/" + strings.Repeat("a", 119)
	env := envMap(map[string]string{"HOME": "/home/u", "XDG_STATE_HOME": long})
	p, err := resolvePaths(&Settings{}, env, tmp)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, fmt.Sprintf("lazyaria-%d", os.Getuid()))
	if !strings.HasPrefix(p.Socket, want+"/") {
		t.Fatalf("socket %q not under %q", p.Socket, want)
	}
}

func TestTmpDirSquatted(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, fmt.Sprintf("lazyaria-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	env := envMap(map[string]string{"HOME": "/home/u", "XDG_STATE_HOME": "/" + strings.Repeat("a", 119)})
	_, err := resolvePaths(&Settings{}, env, tmp)
	if err == nil || !strings.Contains(err.Error(), "unsafe permissions") || !strings.Contains(err.Error(), "lazyaria doctor") {
		t.Fatalf("got %v", err)
	}
}

func TestDownloadDirXDGUserDirs(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# c\nXDG_DOWNLOAD_DIR=\"$HOME/Dl\"\n"
	if err := os.WriteFile(filepath.Join(cfg, "user-dirs.dirs"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	env := envMap(map[string]string{"HOME": home})
	p, err := resolvePaths(&Settings{}, env, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.DownloadDir != home+"/Dl" {
		t.Fatalf("got %q", p.DownloadDir)
	}
	s := &Settings{}
	s.Downloads.Dir = "/data/dl"
	p, _ = resolvePaths(s, env, t.TempDir())
	if p.DownloadDir != "/data/dl" {
		t.Fatalf("got %q", p.DownloadDir)
	}
}

func TestDownloadDirDefault(t *testing.T) {
	home := t.TempDir()
	p, _ := resolvePaths(&Settings{}, envMap(map[string]string{"HOME": home}), t.TempDir())
	if p.DownloadDir != home+"/Downloads" {
		t.Fatalf("got %q", p.DownloadDir)
	}
}

func TestOverrides(t *testing.T) {
	s := &Settings{}
	s.Advanced.SocketPath = "/x/s.sock"
	s.Log.File = "/x/l.log"
	p, _ := resolvePaths(s, envMap(map[string]string{"HOME": "/h"}), t.TempDir())
	if p.Socket != "/x/s.sock" || p.Log != "/x/l.log" {
		t.Fatalf("got %q %q", p.Socket, p.Log)
	}
}

func TestDirsCreated0700(t *testing.T) {
	root := t.TempDir()
	env := envMap(map[string]string{"HOME": root, "XDG_RUNTIME_DIR": filepath.Join(root, "run")})
	p, err := resolvePaths(&Settings{}, env, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirs(p); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.StateDir, p.RuntimeDir} {
		fi, err := os.Stat(d)
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", d, fi, err)
		}
	}
	if err := os.Chmod(p.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirs(p); err == nil {
		t.Fatal("expected error for loose mode")
	}
}
