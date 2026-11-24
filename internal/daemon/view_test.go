package daemon

import (
	"slices"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestLabelTable(t *testing.T) {
	n := func(v int64) *int64 { return &v }
	k := func(v string) *string { return &v }
	for _, tc := range []struct {
		j    store.Job
		want string
	}{
		{store.Job{State: store.Active}, "starting"},
		{store.Job{State: store.Active, TotalBytes: n(100), DoneBytes: n(0)}, "downloading"},
		{store.Job{State: store.Active, DoneBytes: n(5)}, "downloading"},
		{store.Job{State: store.Queued}, "queued"},
		{store.Job{State: store.Paused}, "paused"},
		{store.Job{State: store.RetryWait, ErrorKind: k("rate-limited")}, "retrying"},
		{store.Job{State: store.Seeding}, "seeding"},
		{store.Job{State: store.Done}, "done"},
		{store.Job{State: store.Error, ErrorKind: k("link-expired")}, "link expired"},
		{store.Job{State: store.Error, ErrorKind: k("not-found")}, "not found"},
		{store.Job{State: store.Error, ErrorKind: k("rate-limited")}, "rate limited"},
		{store.Job{State: store.Error, ErrorKind: k("disk-full")}, "disk full"},
		{store.Job{State: store.Error, ErrorKind: k("auth")}, "login needed"},
		{store.Job{State: store.Error, ErrorKind: k("network")}, "network error"},
		{store.Job{State: store.Error, ErrorKind: k("exists")}, "file exists"},
		{store.Job{State: store.Error, ErrorKind: k("dir-missing")}, "folder missing"},
		{store.Job{State: store.Error, ErrorKind: k("http-503")}, "server error 503"},
		{store.Job{State: store.Error, ErrorKind: k("http-unknown")}, "failed"},
		{store.Job{State: store.Error, ErrorKind: k("aria2-30")}, "failed"},
		{store.Job{State: store.Error}, "failed"},
	} {
		if got := Label(&tc.j); got != tc.want {
			kind := "<nil>"
			if tc.j.ErrorKind != nil {
				kind = *tc.j.ErrorKind
			}
			t.Errorf("Label(%s, %s) = %q, want %q", tc.j.State, kind, got, tc.want)
		}
	}
}

// For every state, an action the daemon lists changes the job, and one it
// does not list leaves the job as it was.
func TestActionsMatchManager(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state store.State
		noRef bool
	}{
		{"active", store.Active, false},
		{"queued", store.Queued, false},
		{"queued, not in the engine", store.Queued, true},
		{"paused", store.Paused, false},
		{"retry-wait", store.RetryWait, false},
		{"error", store.Error, false},
		{"seeding", store.Seeding, false},
		{"done", store.Done, false},
	} {
		for _, act := range []string{ActPause, ActResume} {
			r := newRig(t)
			j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
			ref := r.ref(t, j.ID)
			engState := map[store.State]string{store.Active: "active", store.Queued: "waiting", store.Paused: "paused",
				store.Seeding: "active", store.Done: "complete", store.Error: "error", store.RetryWait: "error"}[tc.state]
			r.e.setState(ref, engState)
			before, err := r.st.UpdateJob(bg, j.ID, func(x *store.Job) error {
				x.State = tc.state
				if tc.noRef {
					x.EngineRef = nil
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			listed := slices.Contains(Actions(before), act)
			if act == ActPause {
				err = r.m.Pause(bg, []string{j.ID})
			} else {
				err = r.resume([]string{j.ID})
			}
			if err != nil {
				t.Fatalf("%s: %s: %v", tc.name, act, err)
			}
			after := r.get(t, j.ID)
			changed := after.State != before.State || (after.EngineRef == nil) != (before.EngineRef == nil) ||
				(after.EngineRef != nil && *after.EngineRef != *before.EngineRef)
			if listed != changed {
				t.Errorf("%s: %s listed %v, but the job changed %v (%s -> %s)", tc.name, act, listed, changed, before.State, after.State)
			}
		}
	}
}

// activeAt makes the job active in the fake engine and polls once, so the
// manager has a speed sample for it.
func (r *rig) activeAt(t *testing.T, id string, done, total, down int64) {
	t.Helper()
	r.e.set(r.ref(t, id), func(s *engine.EngineStatus) { s.State, s.Done, s.Total, s.Down = "active", done, total, down })
}

func TestSummaryCountsAndETA(t *testing.T) {
	r := newRig(t)
	a := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	b := r.add(t, AddParams{URIs: []string{"http://example.com/b.iso"}})
	r.add(t, AddParams{URIs: []string{"http://example.com/c.iso"}, StartPaused: true})
	r.activeAt(t, a.ID, 100, 200, 10) // 100 B left at 10 B/s
	r.activeAt(t, b.ID, 100, 400, 30) // 300 B left at 30 B/s
	r.m.poll(bg)
	s := r.m.Snapshot(bg, store.Filter{}).Summary
	if s.Active != 2 || s.Waiting != 1 || s.Attention != 0 || s.Down != 40 || s.ETASeconds == nil || *s.ETASeconds != 10 {
		t.Fatalf("summary %+v (eta %v), want 2 active, 1 waiting, 40 B/s, ETA 10 s", s, s.ETASeconds)
	}

	c := r.add(t, AddParams{URIs: []string{"http://example.com/d.iso"}})
	r.activeAt(t, c.ID, 100, 0, 10) // size unknown
	r.m.poll(bg)
	if s := r.m.Snapshot(bg, store.Filter{}).Summary; s.Active != 3 || s.ETASeconds != nil {
		t.Fatalf("summary %+v with an unknown size, want 3 active and no ETA", s)
	}
}

func TestSnapshotDecorated(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.activeAt(t, j.ID, 10, 100, 5)
	r.m.poll(bg)
	js := r.m.Snapshot(bg, store.Filter{}).Jobs
	if len(js) != 1 {
		t.Fatalf("%d jobs", len(js))
	}
	g := js[0]
	if g.Label != "downloading" || !slices.Equal(g.Actions, []string{ActPause, ActRemove, ActRefresh}) ||
		g.SpeedDown != 5 || g.ETASeconds == nil || *g.ETASeconds != 18 {
		t.Fatalf("snapshot job: label %q actions %v down %d eta %v", g.Label, g.Actions, g.SpeedDown, g.ETASeconds)
	}
}

// Pausing the last active job sends a frame whose totals say nothing runs,
// and the event's job carries its new label and actions.
func TestLifecycleFrame(t *testing.T) {
	r := newRig(t)
	r.m.SetObservers(1)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	r.activeAt(t, j.ID, 10, 100, 5)
	r.m.poll(bg)
	sub := r.bus.Subscribe(true)
	defer sub.Close()
	if err := r.m.Pause(bg, []string{j.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case f := <-sub.Progress():
		if f.Summary.Active != 0 || f.Summary.Waiting != 1 || f.Summary.Down != 0 || f.Summary.ETASeconds != nil {
			t.Fatalf("frame summary %+v after the last active job paused", f.Summary)
		}
	case <-time.After(time.Second):
		t.Fatal("no frame after pause")
	}
	ev := <-sub.Events()
	if ev.Job == nil || ev.Job.Label != "paused" || !slices.Contains(ev.Job.Actions, ActResume) {
		t.Fatalf("event job %+v", ev.Job)
	}
}
