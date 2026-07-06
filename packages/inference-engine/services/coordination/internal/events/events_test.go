package events

import (
	"context"
	"log/slog"
	"testing"
)

func TestNoop(t *testing.T) {
	if err := NewNoop().Publish(context.Background(), Event{RunID: "r1", Status: "queued"}); err != nil {
		t.Fatal(err)
	}
}

func TestSubjectShape(t *testing.T) {
	b := &Bus{prefix: "rommie.events"}
	got := b.subject(Event{RunID: "r1", Status: "queued"})
	if got != "rommie.events.run.r1.queued" {
		t.Fatalf("subject = %q", got)
	}
}

func TestNATSConnectSkipsWhenUnavailable(t *testing.T) {
	_, err := NewNATS("nats://localhost:4222", "rommie.events", slog.New(slog.DiscardHandler))
	if err == nil {
		t.Skip("nats unexpectedly available")
	}
}
