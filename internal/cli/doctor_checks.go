package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"github.com/Pranjal-SB/lazyaria/internal/aria2"
	"github.com/Pranjal-SB/lazyaria/internal/config"
	"github.com/Pranjal-SB/lazyaria/internal/fsinfo"
	"github.com/Pranjal-SB/lazyaria/internal/store"
	"github.com/Pranjal-SB/lazyaria/internal/textutil"
	"github.com/Pranjal-SB/lazyaria/internal/xattr"
)

func checkAria2(s *config.Settings) checkDoc {
	if s.Engine.AttachURL != "" {
		return checkDoc{Check: "aria2c", OK: true, Detail: "attached to " + s.Engine.AttachURL + "; no local aria2c needed"}
	}
	v, err := aria2.CheckVersion(s.Engine.Aria2cPath)
	if err != nil {
		return checkDoc{Check: "aria2c", Detail: err.Error(),
			Fix: "install or upgrade aria2 (≥ 1.35): `" + aria2.InstallHintFrom(osReleasePath) + "`, or set engine.aria2c_path"}
	}
	path, _ := exec.LookPath(s.Engine.Aria2cPath)
	return checkDoc{Check: "aria2c", OK: true, Detail: "aria2 " + v + " at " + path}
}

// ownedPath is nil when path is ours, of the right kind, with mode perm.
func ownedPath(path string, perm os.FileMode, socket bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !ok || int(st.Uid) != os.Getuid():
		return fmt.Errorf("%s is not owned by you", path)
	case socket && fi.Mode().Type() != os.ModeSocket:
		return fmt.Errorf("%s is not a socket", path)
	case !socket && !fi.IsDir():
		return fmt.Errorf("%s is not a directory", path)
	case fi.Mode().Perm() != perm:
		return fmt.Errorf("%s has mode %o, want %o", path, fi.Mode().Perm(), perm)
	}
	return nil
}

func checkRuntimeDir(p config.Paths) checkDoc {
	row := checkDoc{Check: "runtime dir", OK: true, Detail: p.RuntimeDir}
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		row.Detail += " (XDG_RUNTIME_DIR is not set, using a fallback)"
		row.Fix = "log in through systemd-logind or elogind, which set XDG_RUNTIME_DIR"
	}
	if err := ownedPath(p.RuntimeDir, 0o700, false); err != nil && !errors.Is(err, os.ErrNotExist) {
		row.OK, row.Detail, row.Fix = false, err.Error(), "remove "+p.RuntimeDir+"; the daemon recreates it with mode 0700"
	}
	return row
}

func checkSocket(p config.Paths) checkDoc {
	err := ownedPath(p.Socket, 0o600, true)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return checkDoc{Check: "socket", OK: true, Detail: "no socket at " + p.Socket + " (daemon not running)"}
	case err != nil:
		return checkDoc{Check: "socket", Detail: err.Error(), Fix: "stop the daemon and remove " + p.Socket + "; it is recreated with mode 0600"}
	}
	return checkDoc{Check: "socket", OK: true, Detail: p.Socket + ", mode 0600, yours"}
}

func checkDB(p config.Paths) checkDoc {
	v, err := store.SchemaVersion(p.DB)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return checkDoc{Check: "database", OK: true, Detail: "not created yet; the daemon creates " + p.DB}
	case err != nil:
		return checkDoc{Check: "database", Detail: fmt.Sprintf("%s: %v", p.DB, err),
			Fix: "check the permissions of " + p.StateDir + "; moving the file away starts fresh but loses the job history"}
	}
	return checkDoc{Check: "database", OK: true, Detail: fmt.Sprintf("%s, schema v%d", p.DB, v)}
}

func checkDownloadDir(dir string) checkDoc {
	f, err := os.CreateTemp(dir, ".lazyaria-doctor-*")
	if err != nil {
		return checkDoc{Check: "download dir", Detail: err.Error(), Fix: "create " + dir + " or make it writable, or set downloads.dir"}
	}
	f.Close()
	os.Remove(f.Name())
	free, _ := fsinfo.FreeBytes(dir)
	return checkDoc{Check: "download dir", OK: true,
		Detail: fmt.Sprintf("%s, writable, %s free, %s", dir, textutil.Bytes(int64(free), false), fsinfo.TypeName(dir))}
}

func checkXattr(dir string, s *config.Settings) checkDoc {
	if xattr.Supported(dir) {
		return checkDoc{Check: "xattr", OK: true, Detail: "supported; finished files get their origin URL"}
	}
	return checkDoc{Check: "xattr", OK: !s.Privacy.StampOriginXattr, Detail: "user xattrs are not supported in " + dir,
		Fix: "origin URLs cannot be stamped here; set privacy.stamp_origin_xattr = false to silence this"}
}

// checkLogind looks for the directory logind (and elogind) maintain.
// shortcut: no D-Bus round trip, so a logind that is installed but
// refusing inhibitors still shows as present.
func checkLogind(s *config.Settings) checkDoc {
	if runtime.GOOS != "linux" {
		return checkDoc{Check: "logind", OK: true, Detail: "not used on " + runtime.GOOS}
	}
	if _, err := os.Stat("/run/systemd/seats"); err == nil {
		return checkDoc{Check: "logind", OK: true, Detail: "present; downloads keep the machine awake"}
	}
	return checkDoc{Check: "logind", OK: !s.Reliability.InhibitSleep, Detail: "not found; the machine may sleep mid-download",
		Fix: "run systemd-logind or elogind, or set reliability.inhibit_sleep = false to silence this"}
}

func checkTerminal() checkDoc {
	term := os.Getenv("TERM")
	colour := "16 colours"
	switch ct := os.Getenv("COLORTERM"); {
	case os.Getenv("NO_COLOR") != "":
		colour = "no colour (NO_COLOR)"
	case ct == "truecolor" || ct == "24bit":
		colour = "truecolor"
	case strings.Contains(term, "256"):
		colour = "256 colours"
	}
	row := checkDoc{Check: "terminal", OK: term != "" && term != "dumb", Detail: fmt.Sprintf("TERM=%s, %s", term, colour)}
	switch {
	case !row.OK:
		row.Fix = "the TUI needs a real terminal; set TERM (e.g. xterm-256color)"
	case os.Getenv("TMUX") != "":
		row.Fix = "in tmux, `set -g mouse on` and `set -g allow-passthrough on` for mouse, OSC 8 links and OSC 52 copy"
	}
	return row
}

var proxyVars = []string{"http_proxy", "https_proxy", "ftp_proxy", "all_proxy", "no_proxy",
	"HTTP_PROXY", "HTTPS_PROXY", "FTP_PROXY", "ALL_PROXY", "NO_PROXY"}

// checkProxy lists the proxy variables the daemon runs with (G22): its
// own environment where /proc shows it, else ours, which a daemon started
// on demand inherits.
func checkProxy(d daemonView, s *config.Settings) checkDoc {
	env, from := os.Environ(), "this shell"
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", d.pid)); d.pid > 0 && err == nil {
		env, from = strings.Split(string(bytes.TrimRight(b, "\x00")), "\x00"), "daemon"
	}
	var seen []string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		for _, name := range proxyVars {
			if k == name && v != "" {
				seen = append(seen, k+"="+maskProxy(v))
			}
		}
	}
	proxy := s.Network.AllProxy
	if v, ok := d.settings["network.all_proxy"].(string); ok {
		proxy = v
	}
	if proxy != "" {
		seen = append(seen, "network.all_proxy="+maskProxy(proxy))
	}
	if len(seen) == 0 {
		return checkDoc{Check: "proxy", OK: true, Detail: "none (" + from + ")"}
	}
	return checkDoc{Check: "proxy", OK: true, Detail: strings.Join(seen, ", ") + " (" + from + ")"}
}

func optionalTools() []checkDoc {
	var rows []checkDoc
	for _, tool := range []string{"yt-dlp", "ffmpeg", "notify-send", "wl-paste"} {
		row := checkDoc{Check: tool, OK: true, Detail: "not found (optional, for future modules)"}
		if p, err := exec.LookPath(tool); err == nil {
			row.Detail = "found at " + p + " (optional, for future modules)"
		}
		rows = append(rows, row)
	}
	return rows
}
