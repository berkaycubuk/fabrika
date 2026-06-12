package api

import "testing"

func TestHubSubscribe(t *testing.T) {
	h := newHub()
	events, cancel := h.Subscribe(2)

	h.Broadcast(Event{Type: "task.updated", Payload: "a"})
	select {
	case e := <-events:
		if e.Type != "task.updated" || e.Payload != "a" {
			t.Fatalf("unexpected event: %+v", e)
		}
	default:
		t.Fatal("subscriber should have received the event")
	}

	// Overflow drops events without blocking Broadcast.
	for i := 0; i < 5; i++ {
		h.Broadcast(Event{Type: "task.updated"})
	}
	if got := len(events); got != 2 {
		t.Fatalf("expected buffer capped at 2, got %d", got)
	}

	cancel()
	if _, ok := <-events; ok {
		// Drain the two buffered events, then the channel must be closed.
		<-events
		if _, ok := <-events; ok {
			t.Fatal("channel should be closed after cancel")
		}
	}
	// Broadcasting after cancel must not panic (send on closed channel).
	h.Broadcast(Event{Type: "task.updated"})
	cancel() // double cancel is a no-op
}
