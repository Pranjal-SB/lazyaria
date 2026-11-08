package supervisor

import (
	"testing"
	"time"
)

func newPolicy() *Policy {
	return &Policy{MaxBackoff: 30 * time.Second, LoopExits: 5, LoopWindow: 3 * time.Second}
}

func TestPolicyBackoff(t *testing.T) {
	p := newPolicy()
	t0 := time.Unix(1000, 0)
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	for i, w := range want {
		d, giveUp := p.OnExit(t0, t0.Add(10*time.Second))
		if giveUp {
			t.Fatalf("exit %d: gave up on a slow exit", i+1)
		}
		if d != w*time.Second {
			t.Fatalf("exit %d: delay %v, want %v", i+1, d, w*time.Second)
		}
	}
}

func TestPolicyCrashLoop(t *testing.T) {
	p := newPolicy()
	t0 := time.Unix(1000, 0)
	for i := 1; i <= 5; i++ {
		_, giveUp := p.OnExit(t0, t0.Add(2*time.Second))
		if giveUp != (i == 5) {
			t.Fatalf("exit %d: giveUp=%v", i, giveUp)
		}
	}
}

func TestPolicySlowExitResetsLoop(t *testing.T) {
	p := newPolicy()
	t0 := time.Unix(1000, 0)
	for range 4 {
		p.OnExit(t0, t0.Add(time.Second))
	}
	p.OnExit(t0, t0.Add(10*time.Second))
	for i := 1; i <= 4; i++ {
		if _, giveUp := p.OnExit(t0, t0.Add(time.Second)); giveUp {
			t.Fatalf("gave up after a slow exit broke the streak (fast exit %d)", i)
		}
	}
}

func TestPolicyStableRunResetsBackoff(t *testing.T) {
	p := newPolicy()
	t0 := time.Unix(1000, 0)
	for range 4 {
		p.OnExit(t0, t0.Add(10*time.Second))
	}
	if d, _ := p.OnExit(t0, t0.Add(time.Hour)); d != time.Second {
		t.Fatalf("delay after a long run = %v, want 1s", d)
	}
}
