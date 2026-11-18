package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSetGetReset(t *testing.T) {
	startFake(t)
	if code, _, errOut := run(t, Config, "", "set", "ui.units", "si"); code != 0 {
		t.Fatalf("set: exit %d: %s", code, errOut)
	}
	if _, out, _ := run(t, Config, "", "get", "ui.units"); out != "si (override)\n" {
		t.Fatalf("get after set = %q", out)
	}
	if code, _, _ := run(t, Config, "", "reset", "ui.units"); code != 0 {
		t.Fatal("reset", code)
	}
	if _, out, _ := run(t, Config, "", "get", "ui.units"); out != "iec (default)\n" {
		t.Fatalf("get after reset = %q", out)
	}
}

func TestConfigSetInvalid(t *testing.T) {
	startFake(t)
	code, _, errOut := run(t, Config, "", "set", "ui.units", "bogus")
	if code != 2 || !strings.Contains(errOut, "iec|si") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if code, _, _ := run(t, Config, "", "get", "no.such"); code != 2 {
		t.Fatalf("unknown key: exit %d", code)
	}
	if code, _, _ := run(t, Config, "", "frob"); code != 2 {
		t.Fatalf("unknown subcommand: exit %d", code)
	}
}

// Settings nothing reads yet are marked in the list, and setting one warns
// but still saves it (decision 48).
func TestConfigNotUsedYet(t *testing.T) {
	startFake(t)
	_, out, _ := run(t, Config, "", "list")
	want := map[string]bool{"ui.units": true, "remove.confirm": true, "keys.preset": true, "downloads.dir": false, "remove.undo_seconds": false}
	for _, line := range strings.Split(out, "\n") {
		k, _, _ := strings.Cut(line, " ")
		if w, ok := want[k]; ok && strings.HasSuffix(line, "(not used yet)") != w {
			t.Errorf("line %q: want marked %v", line, w)
		}
	}
	code, _, errOut := run(t, Config, "", "set", "ui.units", "si")
	if code != 0 || !strings.Contains(errOut, "ui.units is not used yet") {
		t.Fatalf("set unused: exit %d, stderr %q; want 0 and a warning", code, errOut)
	}
	if _, _, errOut := run(t, Config, "", "set", "downloads.max_concurrent", "2"); errOut != "" {
		t.Fatalf("set used key warned: %q", errOut)
	}
}

// A bad value in a LAZYARIA_* variable is a usage error (spec §10).
func TestBadEnvValueExitsUsage(t *testing.T) {
	startFake(t)
	t.Setenv("LAZYARIA_DOWNLOADS_MAX_CONCURRENT", "lots")
	for _, cmd := range []func(context.Context, []string, io.Writer, io.Writer) int{List, Logs} {
		code, _, errOut := run(t, cmd, "")
		if code != 2 || !strings.Contains(errOut, "LAZYARIA_DOWNLOADS_MAX_CONCURRENT") {
			t.Errorf("exit %d, stderr %q; want 2 naming the variable", code, errOut)
		}
	}
}

// daemon --detach checks the settings before it starts anything, and says
// so like the plain daemon command.
func TestDetachBadSettingExitsUsage(t *testing.T) {
	isolate(t)
	t.Setenv("LAZYARIA_DAEMON_IDLE_EXIT", "banana")
	code, _, errOut := run(t, Daemon, "", "--detach")
	if code != 2 || !strings.HasPrefix(errOut, "lazyaria daemon: daemon.idle_exit from LAZYARIA_DAEMON_IDLE_EXIT") {
		t.Fatalf("exit %d, stderr %q; want 2 and the reason after the command name", code, errOut)
	}
	if code, _, errOut := run(t, Daemon, "", "--detach", "--mode", "bogus"); code != 2 || !strings.HasPrefix(errOut, "lazyaria daemon: ") {
		t.Fatalf("--mode bogus: exit %d, stderr %q", code, errOut)
	}
}

func TestConfigListJSONGolden(t *testing.T) {
	startFake(t)
	code, out, errOut := run(t, Config, "", "list", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	golden(t, "config-list.golden", out)
}

// A config file that is not valid TOML is a config error like a bad value
// (exit 2), and `config path` still says where the file is.
func TestBrokenConfigFile(t *testing.T) {
	p := isolate(t)
	os.MkdirAll(filepath.Dir(p.ConfigFile), 0o700)
	if err := os.WriteFile(p.ConfigFile, []byte("[downloads\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := run(t, List, ""); code != 2 || !strings.Contains(errOut, p.ConfigFile) {
		t.Errorf("list: exit %d, stderr %q; want 2 naming the file", code, errOut)
	}
	if code, out, errOut := run(t, Config, "", "path"); code != 0 || out != p.ConfigFile+"\n" {
		t.Errorf("path: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestConfigPathAndKeys(t *testing.T) {
	startFake(t)
	_, p, _ := resolveLocal(os.Environ(), nil)
	if code, out, _ := run(t, Config, "", "path"); code != 0 || out != p.ConfigFile+"\n" {
		t.Fatalf("path: exit %d %q", code, out)
	}
	if code, out, _ := run(t, Config, "", "keys"); code != 0 || !strings.Contains(out, "keys.preset") || strings.Contains(out, "ui.units") {
		t.Fatalf("keys: exit %d %q", code, out)
	}
}

// fakeEditor writes a script that replaces the file with each body in turn.
func fakeEditor(t *testing.T, bodies ...string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nn=$(cat " + dir + "/n 2>/dev/null || echo 0)\nn=$((n+1))\necho $n > " + dir + "/n\ncp " + dir + "/body$n \"$1\"\n"
	for i, b := range bodies {
		os.WriteFile(filepath.Join(dir, "body"+string(rune('1'+i))), []byte(b), 0o600)
	}
	ed := filepath.Join(dir, "ed")
	os.WriteFile(ed, []byte(script), 0o700)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", ed)
	return dir
}

func TestConfigEditReedit(t *testing.T) {
	p := isolate(t)
	fakeEditor(t, "[ui]\nunits = \"bogus\"\n", "[ui]\nunits = \"si\"\n")
	code, out, errOut := run(t, Config, "y\n", "edit")
	if code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "iec|si") {
		t.Errorf("first save should report the error: %q", errOut)
	}
	b, _ := os.ReadFile(p.ConfigFile)
	if string(b) != "[ui]\nunits = \"si\"\n" {
		t.Errorf("config = %q", b)
	}
}

func TestConfigEditDiscard(t *testing.T) {
	p := isolate(t)
	os.MkdirAll(filepath.Dir(p.ConfigFile), 0o700)
	os.WriteFile(p.ConfigFile, []byte("version = 1\n"), 0o600)
	fakeEditor(t, "[ui]\nunits = \"bogus\"\n")
	if code, _, _ := run(t, Config, "n\n", "edit"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if b, _ := os.ReadFile(p.ConfigFile); string(b) != "version = 1\n" {
		t.Errorf("config changed to %q", b)
	}
}

// A broken file is what edit is for: it opens it rather than refuse.
func TestConfigEditBrokenFile(t *testing.T) {
	for name, body := range map[string]string{"syntax": "daemon = [\n", "value": "[ui]\nunits = \"bogus\"\n"} {
		t.Run(name, func(t *testing.T) {
			p := isolate(t)
			os.MkdirAll(filepath.Dir(p.ConfigFile), 0o700)
			os.WriteFile(p.ConfigFile, []byte(body), 0o600)
			fakeEditor(t, "version = 1\n")
			if code, out, errOut := run(t, Config, "", "edit"); code != 0 {
				t.Fatalf("exit %d: %s %s", code, out, errOut)
			}
			if b, _ := os.ReadFile(p.ConfigFile); string(b) != "version = 1\n" {
				t.Errorf("config = %q", b)
			}
		})
	}
}

func TestConfigValuesVerbatim(t *testing.T) {
	f := startFake(t)
	if code, _, errOut := run(t, Config, "", "set", "engine.aria2_extra_args", "--max-tries=5"); code != 0 {
		t.Fatalf("set extra args: exit %d: %s", code, errOut)
	}
	if f.overrides["engine.aria2_extra_args"] != "--max-tries=5" {
		t.Errorf("overrides = %v", f.overrides)
	}
	code, _, errOut := run(t, Config, "", "set", "downloads.max_concurrent", "-3")
	if code != 2 || !strings.Contains(errOut, "allowed 1 or more") {
		t.Errorf("negative number: exit %d: %s", code, errOut)
	}
}

func TestConfigMasksProxyCredentials(t *testing.T) {
	f := startFake(t)
	f.overrides["network.all_proxy"] = "http://bob:hunter2@proxy:3128"
	for _, args := range [][]string{{"get", "network.all_proxy"}, {"list"}, {"list", "--json"}} {
		_, out, _ := run(t, Config, "", args...)
		if strings.Contains(out, "hunter2") || !strings.Contains(out, "proxy:3128") {
			t.Errorf("config %v: %s", args, out)
		}
	}
}

func TestConfigEditPromptEOF(t *testing.T) {
	isolate(t)
	dir := fakeEditor(t, "[ui]\nunits = \"bogus\"\n")
	if code, _, _ := run(t, Config, "", "edit"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if n, _ := os.ReadFile(filepath.Join(dir, "n")); strings.TrimSpace(string(n)) != "1" {
		t.Errorf("editor ran %s times, want 1", n)
	}
}
