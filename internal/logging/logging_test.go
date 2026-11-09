package logging

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"get https://u:p@h/x?sig=abc failed":    "get https://h/x?… failed",
		"rpc token:deadbeef sent":               "rpc token:*** sent",
		"plain ftp://h/file stays":              "plain ftp://h/file stays",
		`url="http://user@h:8080/a/b?q=1#frag"`: `url="http://h:8080/a/b?…"`,
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactHandler(t *testing.T) {
	var buf bytes.Buffer
	log := newLogger(&buf, Options{})
	log.Info("fetch https://u:p@h/x?sig=abc",
		"uri", "https://h/y?token=1",
		"err", errors.New("dial https://a:b@h/z?k=v: refused"),
		"http-passwd", "hunter2",
		"rpc_secret", "s3cr3t",
		"header", "Authorization: Bearer x")
	out := buf.String()
	for _, leak := range []string{"u:p@", "sig=abc", "token=1", "a:b@", "k=v", "hunter2", "s3cr3t", "Bearer"} {
		if strings.Contains(out, leak) {
			t.Errorf("log line leaks %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"msg":"fetch https://h/x?…"`) {
		t.Errorf("message not redacted as expected: %s", out)
	}
}

func TestRedactOff(t *testing.T) {
	var buf bytes.Buffer
	var on atomic.Bool
	log := newLogger(&buf, Options{Redact: &on})
	log.Info("x", "uri", "https://h/x?sig=abc")
	if !strings.Contains(buf.String(), "sig=abc") {
		t.Errorf("redaction should be off: %s", buf.String())
	}
}

func TestJournaldMode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "lazyaria.log")
	t.Setenv("INVOCATION_ID", "abc")
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	t.Cleanup(func() { stderr = old })

	log, closer := New(Options{File: file})
	log.Info("hello journal")
	closer.Close()
	if !strings.Contains(buf.String(), "hello journal") {
		t.Errorf("stderr = %q, want the log line", buf.String())
	}
	if _, err := os.Stat(file); err == nil {
		t.Errorf("log file %s was written under systemd", file)
	}
}

func TestFileMode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "lazyaria.log")
	t.Setenv("INVOCATION_ID", "")
	log, closer := New(Options{File: file})
	log.Info("hello file")
	closer.Close()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"msg":"hello file"`) {
		t.Errorf("log file = %q, want a JSON line", b)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode = %v, want 0600", fi.Mode().Perm())
	}
}
