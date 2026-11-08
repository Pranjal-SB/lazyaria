package daemon

import (
	"testing"
	"time"
)

func TestBusReliableOrder(t *testing.T) {
	b := NewBus(512)
	s := b.Subscribe(false)
	for i := 0; i < 100; i++ {
		b.Publish(Event{Type: "job.updated"})
	}
	var last uint64
	for i := 0; i < 100; i++ {
		ev := <-s.Events()
		if ev.Seq <= last {
			t.Fatalf("seq %d not after %d", ev.Seq, last)
		}
		last = ev.Seq
	}
}

func TestBusOverflowResync(t *testing.T) {
	b := NewBus(4)
	s := b.Subscribe(false)
	for i := 0; i < 10; i++ {
		b.Publish(Event{Type: "job.updated"})
	}
	ev := <-s.Events()
	if ev.Type != "resync_required" {
		t.Fatalf("got %q, want resync_required", ev.Type)
	}
	select {
	case ev := <-s.Events():
		t.Fatalf("expected drained channel, got %q", ev.Type)
	default:
	}
	b.Publish(Event{Type: "job.added"})
	if ev := <-s.Events(); ev.Type != "job.added" {
		t.Fatalf("events did not resume: %q", ev.Type)
	}
}

func TestBusProgressLatestWins(t *testing.T) {
	b := NewBus(4)
	s := b.Subscribe(true)
	for i := int64(1); i <= 5; i++ {
		b.PublishProgress([]ProgressItem{{ID: "a", Done: i}})
	}
	got := <-s.Progress()
	if got[0].Done != 5 {
		t.Fatalf("got Done=%d, want 5", got[0].Done)
	}
	select {
	case <-s.Progress():
		t.Fatal("expected one value only")
	default:
	}
}

func TestBusProgressOnlyWhenWanted(t *testing.T) {
	b := NewBus(4)
	s := b.Subscribe(false)
	b.PublishProgress([]ProgressItem{{ID: "a"}})
	select {
	case <-s.Progress():
		t.Fatal("unwanted progress delivered")
	case <-time.After(10 * time.Millisecond):
	}
}

func TestBusCloseThenPublish(t *testing.T) {
	b := NewBus(4)
	s := b.Subscribe(true)
	s.Close()
	s.Close()
	b.Publish(Event{Type: "banner"})
	b.PublishProgress(nil)
	if _, ok := <-s.Events(); ok {
		t.Fatal("events channel should be closed")
	}
}
