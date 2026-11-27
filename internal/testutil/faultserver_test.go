package testutil

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *FaultServer {
	t.Helper()
	return NewFaultServer(t, map[string][]byte{
		"a.bin":    RandBytes(512 << 10),
		"slow.bin": RandBytes(128 << 10),
		"exp.bin":  RandBytes(512 << 10),
		"nr.bin":   RandBytes(64 << 10),
		"drop.bin": RandBytes(512 << 10),
		"flip.bin": RandBytes(4 << 10),
	})
}

func get(t *testing.T, url string, hdr http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestFaultModes(t *testing.T) {
	srv := newTestServer(t)

	for _, tc := range []struct {
		mode Mode
		code int
	}{
		{Mode403, 403},
		{Mode404, 404},
		{Mode410, 410},
		{Mode429, 429},
		{Mode503, 503},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			resp := get(t, srv.URL("a.bin", tc.mode), nil)
			if resp.StatusCode != tc.code {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.code)
			}
			if tc.mode == Mode429 && resp.Header.Get("Retry-After") != "1" {
				t.Fatalf("Retry-After = %q, want %q", resp.Header.Get("Retry-After"), "1")
			}
		})
	}

	t.Run("ok", func(t *testing.T) {
		resp := get(t, srv.URL("a.bin", ModeOK), nil)
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || !bytes.Equal(body, srv.files["a.bin"]) {
			t.Fatalf("status = %d, body mismatch = %v", resp.StatusCode, !bytes.Equal(body, srv.files["a.bin"]))
		}
	})

	t.Run("slow", func(t *testing.T) {
		start := time.Now()
		resp := get(t, srv.URL("slow.bin", ModeSlow), nil)
		body, err := io.ReadAll(resp.Body)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, srv.files["slow.bin"]) {
			t.Fatal("slow body mismatch")
		}
		if elapsed < 900*time.Millisecond {
			t.Fatalf("128 KiB took %v, want >= 900ms at 128 KiB/s", elapsed)
		}
	})

	t.Run("expire", func(t *testing.T) {
		resp := get(t, srv.URL("exp.bin", ModeExpire), http.Header{"Range": {"bytes=0-262143"}})
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusPartialContent || len(body) != 256<<10 {
			t.Fatalf("first range: status = %d, len = %d; want 206, 256 KiB", resp.StatusCode, len(body))
		}
		resp2 := get(t, srv.URL("exp.bin", ModeExpire), http.Header{"Range": {"bytes=262144-524287"}})
		if resp2.StatusCode != http.StatusForbidden {
			t.Fatalf("second range: status = %d, want 403", resp2.StatusCode)
		}
	})

	t.Run("drop", func(t *testing.T) {
		resp := get(t, srv.URL("drop.bin", ModeDrop), nil)
		body, err := io.ReadAll(resp.Body)
		if err == nil {
			t.Fatal("expected read error from dropped connection")
		}
		if len(body) != 128<<10 {
			t.Fatalf("got %d bytes before drop, want 128 KiB", len(body))
		}
	})

	t.Run("norange", func(t *testing.T) {
		resp := get(t, srv.URL("nr.bin", ModeNoRange), http.Header{"Range": {"bytes=0-99"}})
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			t.Fatalf("Content-Length = %q, want none", cl)
		}
		if !bytes.Equal(body, srv.files["nr.bin"]) {
			t.Fatal("norange body should be the full file")
		}
	})
}

func TestSetModeAndHits(t *testing.T) {
	srv := newTestServer(t)
	name := "flip.bin"

	get(t, srv.URL(name, ModeOK), nil)
	srv.SetMode(name, Mode503)
	if resp := get(t, srv.URL(name, ModeOK), nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("after SetMode 503: status = %d", resp.StatusCode)
	}
	srv.SetMode(name, ModeOK)
	if resp := get(t, srv.URL(name, Mode429), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("after SetMode ok: status = %d", resp.StatusCode)
	}
	if got := srv.Hits(name); got != 3 {
		t.Fatalf("Hits = %d, want 3", got)
	}
	if got := srv.Hits("missing.bin"); got != 0 {
		t.Fatalf("Hits(missing) = %d, want 0", got)
	}
}

// A gate holds the body at a file offset, for a ranged request too, until
// it is raised; the bytes sent are the file's.
func TestGate(t *testing.T) {
	srv := newTestServer(t)
	name, data := "a.bin", srv.files["a.bin"]
	srv.SetGate(name, 1000)
	resp := get(t, srv.URL(name, ModeOK), http.Header{"Range": {"bytes=100-"}})
	got := make([]byte, 900)
	if _, err := io.ReadFull(resp.Body, got); err != nil || !bytes.Equal(got, data[100:1000]) {
		t.Fatalf("before the gate: %v", err)
	}
	more := make(chan error, 1)
	go func() {
		b, err := io.ReadAll(resp.Body)
		if err == nil && !bytes.Equal(b, data[1000:]) {
			err = io.ErrUnexpectedEOF
		}
		more <- err
	}()
	select {
	case err := <-more:
		t.Fatalf("read past the gate: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	srv.SetGate(name, -1)
	if err := <-more; err != nil {
		t.Fatalf("after the gate opened: %v", err)
	}
}

func TestRandBytesDeterministic(t *testing.T) {
	a := RandBytes(1024)
	b := RandBytes(1024)
	if len(a) != 1024 || !bytes.Equal(a, b) {
		t.Fatal("RandBytes must return the same bytes for the same length")
	}
}

func TestSHA256File(t *testing.T) {
	p := filepath.Join(t.TempDir(), "abc.txt")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := SHA256File(t, p); got != want {
		t.Fatalf("SHA256File = %s, want %s", got, want)
	}
}

func TestRequireAria2(t *testing.T) {
	if p := RequireAria2(t); filepath.Base(p) != "aria2c" {
		t.Fatalf("RequireAria2 = %q, want path ending in aria2c", p)
	}
}
