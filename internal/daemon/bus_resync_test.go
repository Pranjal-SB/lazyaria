package daemon

import "testing"

func TestBusResyncGetsFreshSeq(t *testing.T) {
	b := NewBus(2)
	s := b.Subscribe(false)
	for i := 0; i < 3; i++ { // the third overflows the queue
		b.Publish(Event{Type: "job.updated"})
	}
	ev := <-s.Events()
	if ev.Type != "resync_required" || ev.Seq != 4 {
		t.Fatalf("got %s seq %d; want resync_required with its own seq 4", ev.Type, ev.Seq)
	}
}
