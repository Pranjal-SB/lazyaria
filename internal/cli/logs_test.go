package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const logLine = `{"time":"2026-11-07T10:00:00.5Z","level":"INFO","msg":"daemon started","pid":42,"socket":"/r/x.sock","note":"two words"}` + "\n"

func TestLogsHuman(t *testing.T) {
	p := isolate(t)
	os.MkdirAll(p.StateDir, 0o700)
	os.WriteFile(p.Log, []byte(logLine+"not json\n"), 0o600)
	code, out, errOut := run(t, Logs, "")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	want := `INFO  daemon started pid=42 socket=/r/x.sock note="two words"`
	if !strings.Contains(out, want) || !strings.Contains(out, "not json") {
		t.Errorf("logs output:\n%s\nwant line containing %s", out, want)
	}
}

func TestLogsMissing(t *testing.T) {
	isolate(t)
	if code, _, errOut := run(t, Logs, ""); code != 1 || !strings.Contains(errOut, "journalctl") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestLogsFollow(t *testing.T) {
	p := isolate(t)
	os.MkdirAll(p.StateDir, 0o700)
	os.WriteFile(p.Log, nil, 0o600)
	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuf
	done := make(chan int)
	go func() { done <- Logs(ctx, []string{"-f"}, &out, &out) }()
	time.Sleep(100 * time.Millisecond)
	f, _ := os.OpenFile(p.Log, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(logLine)
	f.Close()
	end := time.Now().Add(3 * time.Second)
	for !strings.Contains(out.String(), "daemon started") && time.Now().Before(end) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(out.String(), "daemon started") {
		t.Errorf("follow did not show the new line: %q", out.String())
	}
}
