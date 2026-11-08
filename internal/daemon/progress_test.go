package daemon

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Pranjal-SB/lazyaria/internal/engine"
	"github.com/Pranjal-SB/lazyaria/internal/store"
)

func TestProgressEMA(t *testing.T) {
	p := newProgress()
	t0 := time.Unix(1000, 0)
	var it ProgressItem
	for i := 0; i <= 10; i++ {
		it = p.observe("a", t0.Add(time.Duration(i)*time.Second), engine.EngineStatus{Down: 100, Done: int64(i) * 100})
	}
	if it.ETA != nil {
		t.Fatalf("eta %v with unknown total", *it.ETA)
	}
	before := float64(it.Down)
	for i := 11; i <= 15; i++ {
		it = p.observe("a", t0.Add(time.Duration(i)*time.Second), engine.EngineStatus{Down: 0})
	}
	if ratio := float64(it.Down) / before; math.Abs(ratio-0.5) > 0.05*0.5 {
		t.Fatalf("after one half-life: %v -> %v (ratio %.3f)", before, it.Down, ratio)
	}
	it = p.observe("b", t0, engine.EngineStatus{Down: 100, Done: 0, Total: 1000})
	if it.ETA == nil || *it.ETA != 10*time.Second {
		t.Fatalf("eta %v, want 10s", it.ETA)
	}
}

func TestSpeedRing(t *testing.T) {
	p := newProgress()
	t0 := time.Unix(1000, 0)
	for i := 0; i < 90; i++ {
		p.sample(t0.Add(time.Duration(i)*time.Second), int64(i), 0)
	}
	s := p.samples()
	if len(s) != 61 || s[0].Down != 29 || s[len(s)-1].Down != 89 {
		t.Fatalf("ring holds %d samples, first %d", len(s), s[0].Down)
	}
}

func TestPollOnlyWhenObserved(t *testing.T) {
	r := newRig(t)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.setState(ref, "active")
	r.event(ref, engine.KindStart)
	if got := r.get(t, j.ID); got.State != store.Active {
		t.Fatalf("state %s", got.State)
	}
	run := func(observers int) int {
		ctx, cancel := context.WithCancel(bg)
		r.m.SetObservers(observers)
		_, before := r.e.count()
		done := make(chan struct{})
		go func() { r.m.Run(ctx); close(done) }()
		time.Sleep(3 * time.Second)
		cancel()
		<-done
		_, after := r.e.count()
		return after - before
	}
	if n := run(0); n > 1 {
		t.Fatalf("unobserved: %d status calls in 3s", n)
	}
	if n := run(1); n < 2 {
		t.Fatalf("observed: %d status calls in 3s", n)
	}
}

func TestPollPublishesProgress(t *testing.T) {
	r := newRig(t)
	sub := r.bus.Subscribe(true)
	j := r.add(t, AddParams{URIs: []string{"http://example.com/a.iso"}})
	ref := r.ref(t, j.ID)
	r.e.set(ref, func(s *engine.EngineStatus) { s.State, s.Done, s.Total, s.Down = "active", 10, 100, 5 })
	r.m.poll(bg)
	select {
	case items := <-sub.Progress():
		if len(items) != 1 || items[0].ID != j.ID || items[0].Done != 10 {
			t.Fatalf("items %+v", items)
		}
	case <-time.After(time.Second):
		t.Fatal("no progress")
	}
	_, jobs, speeds := r.m.Snapshot(bg, false, 0)
	if len(jobs) != 1 || jobs[0].DoneBytes == nil || *jobs[0].DoneBytes != 10 || len(speeds) != 1 {
		t.Fatalf("snapshot jobs %+v speeds %v", jobs, speeds)
	}
	if r.m.ActiveCount() != 1 {
		t.Fatalf("active count %d", r.m.ActiveCount())
	}
}

func TestPurgeOnStart(t *testing.T) {
	r := newRig(t)
	old := r.clk.now().Add(-31 * 24 * time.Hour)
	recent := r.clk.now().Add(-24 * time.Hour)
	a := r.insert(t, store.Removed, "", func(j *store.Job) { j.RemovedAt = &old })
	b := r.insert(t, store.Removed, "", func(j *store.Job) { j.RemovedAt = &recent })
	r.m.s.History.KeepDays = 7
	oldDone := r.insert(t, store.Done, "", func(j *store.Job) { j.FinishedAt = &old })
	newDone := r.insert(t, store.Done, "", func(j *store.Job) { j.FinishedAt = &recent })
	ctx, cancel := context.WithTimeout(bg, 200*time.Millisecond)
	defer cancel()
	r.m.Run(ctx)
	if _, err := r.st.GetJob(bg, a.ID); err == nil {
		t.Fatal("old removed job not purged")
	}
	if _, err := r.st.GetJob(bg, oldDone.ID); err == nil {
		t.Fatal("finished job past history.keep_days not purged")
	}
	r.get(t, b.ID)
	r.get(t, newDone.ID)
}
