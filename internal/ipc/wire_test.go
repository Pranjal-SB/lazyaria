package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/daemon"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func keys(t *testing.T, v any) []string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(m))
}

func sameKeys(t *testing.T, name string, got []string, want string) {
	t.Helper()
	w := strings.Fields(want)
	slices.Sort(w)
	if !slices.Equal(got, w) {
		t.Errorf("%s keys changed:\n got %v\nwant %v", name, got, w)
	}
}

// These pin the wire format: a failure here means a protocol change.
func TestWireKeys(t *testing.T) {
	job := &store.Job{ID: "a", Torrent: []byte("d8:announce")}
	sameKeys(t, "Job", keys(t, job), `id kind engine engine_ref state error_kind error_code error_msg name uris
		dir out dir_dev total_bytes done_bytes options limit_down limit_up category queue priority
		retry_count retry_at referrer created_at started_at finished_at removed_at`)
	sameKeys(t, "Event", keys(t, daemon.Event{Seq: 1, Type: "job.added", Job: job, Data: map[string]any{"x": 1}}), "seq type job data")
	sameKeys(t, "Event without job", keys(t, daemon.Event{Seq: 1, Type: "resync_required"}), "seq type")

	eta := 90*time.Second + 400*time.Millisecond
	sameKeys(t, "ProgressItem", keys(t, daemon.ProgressItem{ID: "a", ETA: &eta}), "id done total speed_down speed_up conns eta_seconds")
	b, _ := json.Marshal(daemon.ProgressItem{ID: "a", ETA: &eta})
	if !strings.Contains(string(b), `"eta_seconds":90`) {
		t.Errorf("ETA not in whole seconds: %s", b)
	}
	b, _ = json.Marshal(daemon.ProgressItem{ID: "a"})
	if !strings.Contains(string(b), `"eta_seconds":null`) {
		t.Errorf("unknown ETA not null: %s", b)
	}
	var back daemon.ProgressItem
	if err := json.Unmarshal([]byte(`{"id":"a","speed_down":5,"eta_seconds":90}`), &back); err != nil || back.Down != 5 || back.ETA == nil || *back.ETA != 90*time.Second {
		t.Errorf("ProgressItem round trip = %+v, %v", back, err)
	}

	sameKeys(t, "AddParams", keys(t, daemon.AddParams{URIs: []string{"u"}, Torrent: []byte{1}, Dir: "d", Out: "o",
		Options: map[string]string{"k": "v"}, StartPaused: true, Force: true, Total: 1}),
		"uris torrent_b64 dir out options start_paused force total")
	sameKeys(t, "DuplicateError", keys(t, &daemon.DuplicateError{Existing: job, Path: "p"}), "existing_job path")
	sameKeys(t, "NoSpaceError", keys(t, &daemon.NoSpaceError{Dir: "d"}), "dir need free")
	sameKeys(t, "SpeedSample", keys(t, daemon.SpeedSample{}), "at speed_down speed_up")
}

func TestRedactsErrorData(t *testing.T) {
	ctx := context.Background()
	secret := func() *store.Job {
		return &store.Job{ID: "old", Options: map[string]string{"http-passwd": "hunter2", "header": "Cookie: s=1"}}
	}
	check := func(name string, j *store.Job) {
		t.Helper()
		if j == nil || j.Options["http-passwd"] != "***" || j.Options["header"] != "***" {
			t.Errorf("%s existing job not redacted: %+v", name, j)
		}
	}

	f := newFake()
	orig := secret()
	f.addErr = &daemon.DuplicateError{Existing: orig}
	c, _ := serve(t, f)
	_, err := c.Add(ctx, daemon.AddParams{URIs: []string{"https://example.com/a"}})
	dup, ok := errors.AsType[*daemon.DuplicateError](err)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	check("duplicate", dup.Existing)
	if orig.Options["http-passwd"] != "hunter2" {
		t.Error("redaction mutated the daemon's job")
	}

	f = newFake()
	f.addErr = &daemon.SkippedError{Existing: secret()}
	c, _ = serve(t, f)
	j, err := c.Add(ctx, daemon.AddParams{URIs: []string{"https://example.com/a"}})
	if _, ok := errors.AsType[*daemon.SkippedError](err); !ok {
		t.Fatalf("err = %v", err)
	}
	check("skipped", j)
}
