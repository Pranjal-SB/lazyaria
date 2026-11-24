package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A panic in a command restores the terminal and leaves a crash log.
func TestPanicRestoresTerminalAndLogs(t *testing.T) {
	d := newFakeDaemon(job(1, "a.iso", store.Active, daemon.ActPause))
	d.pause = func([]string) { panic("pause exploded") }
	o := options(t, d)
	in, keys := io.Pipe()
	out := &lockedBuf{}
	o.Program = []tea.ProgramOption{tea.WithInput(in), tea.WithOutput(out), tea.WithoutSignals(), tea.WithWindowSize(80, 24)}
	done := make(chan error, 1)
	go func() { _, err := Run(context.Background(), o); done <- err }()
	var err error
	// space does nothing until the list arrives, so keep pressing it.
	for tick := time.Tick(50 * time.Millisecond); ; {
		select {
		case err = <-done:
		case <-tick:
			go keys.Write([]byte(" "))
			continue
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after the panic")
		}
		break
	}
	keys.Close()
	if !errors.Is(err, tea.ErrProgramPanic) {
		t.Fatalf("Run returned %v, want ErrProgramPanic", err)
	}
	if !strings.Contains(out.String(), "\x1b[?1049l") {
		t.Fatal("the alternate screen was not left")
	}
	logs, _ := filepath.Glob(filepath.Join(o.CrashDir, "crash-*.log"))
	if len(logs) != 1 {
		t.Fatalf("crash logs %v, want one", logs)
	}
	b, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(b), "pause exploded") || !strings.Contains(string(b), "goroutine") {
		t.Fatalf("crash log lacks the panic or the stack:\n%s", b)
	}
}
