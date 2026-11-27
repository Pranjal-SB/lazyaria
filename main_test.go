package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Pranjal-SB/lazyaria/internal/cli"
)

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"lazyaria", "version"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, errb.String())
	}
	if got, want := out.String(), "lazyaria dev (protocol 1)\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

// -h prints help and exits 0 for every command and subcommand.
func TestHelpFlagExitsZero(t *testing.T) {
	var cmds [][]string
	for name := range commands {
		cmds = append(cmds, []string{name})
	}
	for _, sub := range []string{"list", "keys", "get", "set", "reset", "path", "edit"} {
		cmds = append(cmds, []string{"config", sub})
	}
	cmds = append(cmds, []string{"service", "install"}, []string{"service", "uninstall"})
	for _, c := range cmds {
		for _, h := range []string{"-h", "--help"} {
			var out, errb bytes.Buffer
			if code := run(context.Background(), append(append([]string{"lazyaria"}, c...), h), &out, &errb); code != 0 {
				t.Errorf("%v %s: exit %d, want 0 (stderr: %q)", c, h, code, errb.String())
			}
		}
	}
}

// A command that takes no arguments refuses one with a usage error.
func TestUnexpectedArgument(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root+"/c")
	t.Setenv("XDG_STATE_HOME", root+"/s")
	t.Setenv("XDG_RUNTIME_DIR", root+"/r")
	t.Setenv("LAZYARIA_DAEMON_AUTO_START", "false")
	for _, c := range []string{"list", "status", "doctor", "logs", "version", "daemon"} {
		var out, errb bytes.Buffer
		code := run(context.Background(), []string{"lazyaria", c, "zz"}, &out, &errb)
		if code != 2 || !strings.Contains(errb.String(), `unexpected argument "zz"`) {
			t.Errorf("%s zz: exit %d, stderr %q; want 2 naming the argument", c, code, errb.String())
		}
	}
}

func TestRunUnknown(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"lazyaria", "nope"}, &out, &errb)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	msg := errb.String()
	if !strings.Contains(msg, `unknown command "nope"`) {
		t.Errorf("stderr %q missing unknown command message", msg)
	}
	if !strings.Contains(msg, "lazyaria help") {
		t.Errorf("stderr %q missing fix hint", msg)
	}
}

func TestAddShorthand(t *testing.T) {
	dir := t.TempDir()
	tor := filepath.Join(dir, "x.torrent")
	if err := os.WriteFile(tor, []byte("d4:infod4:name1:xee"), 0o644); err != nil {
		t.Fatal(err)
	}
	for arg, want := range map[string]bool{
		"https://example.com/a.iso":      true,
		"ftp://example.com/a":            true,
		"magnet:?xt=urn:btih:abc":        true,
		"MAGNET:?xt=urn:btih:abc":        true,
		"-":                              true,
		tor:                              true,
		filepath.Join(dir, "no.torrent"): false,
		dir:                              false,
		"lsit":                           false,
		"ad":                             false,
		"--help":                         false,
		"example.com/a.iso":              false,
	} {
		if got := addShorthand(arg); got != want {
			t.Errorf("addShorthand(%q) = %v, want %v", arg, got, want)
		}
	}
}

// "laria <link>" runs add with every argument; a typo of a command is
// still an unknown command.
func TestRunAddShorthand(t *testing.T) {
	var out, errb bytes.Buffer
	// An unknown flag stops add before it needs the daemon.
	code := run(context.Background(), []string{"laria", "https://example.com/a.iso", "--bogus"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "-bogus") || strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("shorthand: exit %d, stderr %q; want add's flag error", code, errb.String())
	}
	cli.Prog = "lazyaria"
	errb.Reset()
	code = run(context.Background(), []string{"lazyaria", "lsit"}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), `unknown command "lsit"`) {
		t.Fatalf("typo: exit %d, stderr %q", code, errb.String())
	}
}

// Help, usage and hints name the binary as it was invoked, lazyaria or
// its short alias laria.
func TestHelpUsesInvokedName(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"/usr/bin/laria", "help"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "usage: laria <command>") {
		t.Fatalf("help as laria: exit %d, stdout %q", code, out.String())
	}
	errb.Reset()
	run(context.Background(), []string{"laria", "nope"}, &out, &errb)
	if !strings.Contains(errb.String(), `Run "laria help"`) {
		t.Fatalf("unknown command as laria: stderr %q", errb.String())
	}
	errb.Reset()
	run(context.Background(), []string{"laria", "pause"}, &out, &errb)
	if !strings.HasPrefix(errb.String(), "laria pause: ") {
		t.Fatalf("pause as laria: stderr %q", errb.String())
	}
	cli.Prog = "lazyaria"
}

// With no command and no terminal, scripts see usage and exit 2 as before;
// only a terminal opens the interface.
func TestNoArgsWithoutTerminalPrintsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"lazyaria"}, &out, &errb)
	if code != 2 || !strings.HasPrefix(errb.String(), "usage: lazyaria") || out.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 2 and usage on stderr", code, out.String(), errb.String())
	}
}
