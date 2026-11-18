package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
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
