package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// maxSocketPath is sun_path on macOS/BSD: 104 bytes including the NUL, so 103 usable.
const maxSocketPath = 104

// Paths holds every resolved filesystem location lazyaria uses.
type Paths struct {
	ConfigFile, StateDir, RuntimeDir, Socket, Lock, AriaConf, DB, Log, DHT string
	DownloadDir                                                            string
}

// ResolvePaths computes all paths from settings and the environment.
func ResolvePaths(s *Settings, env func(string) string) (Paths, error) {
	return resolvePaths(s, env, "/tmp")
}

// resolvePaths takes the tmp root as a parameter so tests stay out of /tmp.
func resolvePaths(s *Settings, env func(string) string, tmpRoot string) (Paths, error) {
	home := env("HOME")
	if home == "" {
		return Paths{}, errors.New("HOME is not set; set it or run `lazyaria doctor`")
	}
	cfgHome := xdgDir(env, "XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	stateHome := xdgDir(env, "XDG_STATE_HOME", filepath.Join(home, ".local", "state"))

	p := Paths{
		ConfigFile: filepath.Join(cfgHome, "lazyaria", "config.toml"),
		StateDir:   filepath.Join(stateHome, "lazyaria"),
	}
	p.DB = filepath.Join(p.StateDir, "lazyaria.db")
	p.Log = filepath.Join(p.StateDir, "lazyaria.log")
	p.DHT = filepath.Join(p.StateDir, "dht.dat")
	if s.Log.File != "" {
		p.Log = s.Log.File
	}

	if rt := env("XDG_RUNTIME_DIR"); rt != "" && filepath.IsAbs(rt) {
		p.RuntimeDir = filepath.Join(rt, "lazyaria")
	} else {
		p.RuntimeDir = filepath.Join(p.StateDir, "run")
	}
	if len(p.RuntimeDir+"/lazyaria.sock") >= maxSocketPath && s.Advanced.SocketPath == "" {
		dir := filepath.Join(tmpRoot, fmt.Sprintf("lazyaria-%d", os.Getuid()))
		if err := checkTmpDir(dir); err != nil {
			return Paths{}, err
		}
		p.RuntimeDir = dir
	}
	p.Socket = filepath.Join(p.RuntimeDir, "lazyaria.sock")
	p.Lock = filepath.Join(p.RuntimeDir, "daemon.lock")
	p.AriaConf = filepath.Join(p.RuntimeDir, "aria2.conf")
	if s.Advanced.SocketPath != "" {
		p.Socket = s.Advanced.SocketPath
		if len(p.Socket) >= maxSocketPath {
			return Paths{}, fmt.Errorf("advanced.socket_path is %d bytes, limit is %d; use a shorter path", len(p.Socket), maxSocketPath-1)
		}
	}

	p.DownloadDir = s.Downloads.Dir
	if p.DownloadDir == "" {
		p.DownloadDir = userDownloadDir(cfgHome, home)
	}
	return p, nil
}

// xdgDir follows the XDG spec: relative or empty values are ignored.
func xdgDir(env func(string) string, key, def string) string {
	if v := env(key); v != "" && filepath.IsAbs(v) {
		return v
	}
	return def
}

// userDownloadDir reads XDG_DOWNLOAD_DIR from user-dirs.dirs, else ~/Downloads.
func userDownloadDir(cfgHome, home string) string {
	def := filepath.Join(home, "Downloads")
	b, err := os.ReadFile(filepath.Join(cfgHome, "user-dirs.dirs"))
	if err != nil {
		return def
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(k) != "XDG_DOWNLOAD_DIR" {
			continue
		}
		v = filepath.Clean(expandHome(unquoteValue(v), home))
		if filepath.IsAbs(v) {
			return v
		}
	}
	return def
}

var homeVar = regexp.MustCompile(`\$\{HOME\}|\$HOME(/|$)`)

// expandHome only expands $HOME when it ends a path component.
func expandHome(v, home string) string {
	return homeVar.ReplaceAllStringFunc(v, func(m string) string {
		if m == "${HOME}" {
			return home
		}
		return home + strings.TrimPrefix(m, "$HOME")
	})
}

// unquoteValue takes a quoted value up to its closing quote, or a bare word up to a space or '#'.
func unquoteValue(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, `"`) {
		v = v[1:]
		if i := strings.IndexByte(v, '"'); i >= 0 {
			v = v[:i]
		}
		return v
	}
	if i := strings.IndexAny(v, " \t#"); i >= 0 {
		v = v[:i]
	}
	return v
}

func checkTmpDir(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil // EnsureDirs creates it
	}
	if err != nil {
		return err
	}
	return verifyDir(dir, fi)
}

func verifyDir(dir string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Getuid() || fi.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s has unsafe permissions (must be a directory owned by you with mode 0700, not a symlink); remove or fix it, or run `lazyaria doctor`", dir)
	}
	return nil
}

// EnsureDirs creates the state and runtime dirs (0700). Symlinked dirs are followed and
// loose modes on our own dirs are tightened; a foreign owner is an error. The /tmp
// fallback is held to the strict no-symlink, exact-0700 rule.
func EnsureDirs(p Paths) error {
	uid := os.Getuid()
	for _, d := range []string{p.StateDir, p.RuntimeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
		if filepath.Base(d) == fmt.Sprintf("lazyaria-%d", uid) {
			fi, err := os.Lstat(d)
			if err != nil {
				return err
			}
			if err := verifyDir(d, fi); err != nil {
				return err
			}
			continue
		}
		fi, err := os.Stat(d)
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !fi.IsDir() || !ok {
			return fmt.Errorf("%s is not a directory; remove it or run `lazyaria doctor`", d)
		}
		if int(st.Uid) != uid {
			return fmt.Errorf("%s is owned by uid %d, not you (uid %d); chown it to yourself or remove it, or run `lazyaria doctor`", d, st.Uid, uid)
		}
		if fi.Mode().Perm() != 0o700 {
			if err := os.Chmod(d, 0o700); err != nil {
				return fmt.Errorf("chmod %s: %w", d, err)
			}
		}
	}
	return nil
}
