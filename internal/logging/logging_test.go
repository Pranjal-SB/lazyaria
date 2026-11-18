package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// Durations read as "1m1.1s", not as nanoseconds.
func TestDurationReadable(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, Options{}).Info("idle", "uptime", 61100*time.Millisecond, "group", slog.GroupValue(slog.Duration("wait", 30*time.Second)))
	if out := buf.String(); !strings.Contains(out, `"uptime":"1m1.1s"`) || !strings.Contains(out, `"wait":"30s"`) {
		t.Fatalf("durations not readable: %s", out)
	}
}

func TestRedactOff(t *testing.T) {
	var buf bytes.Buffer
	var on atomic.Bool
	log := newLogger(&buf, Options{Redact: &on})
	log.Info("call token:abc", "uri", "https://u:p@h/x?sig=abc", "http-passwd", "hunter2")
	out := buf.String()
	if !strings.Contains(out, "sig=abc") || !strings.Contains(out, "u:p@") {
		t.Errorf("URL stripping should be off: %s", out)
	}
	if strings.Contains(out, "token:abc") || strings.Contains(out, "hunter2") {
		t.Errorf("secrets must be masked even with redact_logs off: %s", out)
	}
}

func TestRedactNested(t *testing.T) {
	var buf bytes.Buffer
	log := newLogger(&buf, Options{})
	log.Info("x",
		slog.Group("job", "uri", "https://u:p@h/x?q=1", "header", "Cookie: a=1"),
		"opts", map[string]string{"header": "Bearer zz", "dir": "/d"},
		"uris", []string{"https://h/a?sig=1"},
		"req", struct{ URI string }{"https://h/b?sig=2"})
	out := buf.String()
	for _, leak := range []string{"u:p@", "q=1", "a=1", "Bearer", "sig=1", "sig=2"} {
		if strings.Contains(out, leak) {
			t.Errorf("log line leaks %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, `"dir":"/d"`) {
		t.Errorf("harmless map entries should stay: %s", out)
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
