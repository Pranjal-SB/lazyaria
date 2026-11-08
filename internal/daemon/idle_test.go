package daemon

import (
	"testing"
	"time"
)

func TestIdleExit(t *testing.T) {
	now := time.Unix(1000, 0)
	i := NewIdle(30*time.Second, func() time.Time { return now })
	got := i.Update(0, 0, false)
	if got == nil || !got.Equal(now.Add(30*time.Second)) {
		t.Fatalf("got %v", got)
	}
	now = now.Add(10 * time.Second)
	if got2 := i.Update(0, 0, false); got2 == nil || !got2.Equal(*got) {
		t.Fatalf("deadline slid: %v", got2)
	}
	if i.Update(0, 1, false) != nil {
		t.Fatal("client should cancel exit")
	}
	if i.Update(0, 0, true) != nil {
		t.Fatal("always should cancel exit")
	}
	if i.Update(2, 0, false) != nil {
		t.Fatal("active jobs should cancel exit")
	}
}

func TestIdleResetsOnActivity(t *testing.T) {
	now := time.Unix(1000, 0)
	i := NewIdle(30*time.Second, func() time.Time { return now })
	first := i.Update(0, 0, false)
	now = now.Add(20 * time.Second)
	i.Update(1, 0, false)
	now = now.Add(5 * time.Second)
	second := i.Update(0, 0, false)
	if !second.Equal(now.Add(30*time.Second)) || second.Equal(*first) {
		t.Fatalf("deadline not reset: %v vs %v", second, first)
	}
}
