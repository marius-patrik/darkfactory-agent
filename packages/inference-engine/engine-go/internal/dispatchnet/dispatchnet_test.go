package dispatchnet

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestNoopDispatcher(t *testing.T) {
	d := NewNoop()
	ctx := context.Background()

	if err := d.Publish(ctx, TopicDispatch, map[string]string{"task": "test"}); err != nil {
		t.Fatalf("noop publish: %v", err)
	}
	if err := d.Subscribe(ctx, TopicDispatch, func(ctx context.Context, msg Message) error { return nil }); err != nil {
		t.Fatalf("noop subscribe: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("noop close: %v", err)
	}
}

func TestNATSDispatcher_Connect(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := NewNATS("nats://localhost:4222", "agent-test", "agents.dispatch", log)
	if err == nil {
		t.Skip("nats unexpectedly available")
	}
	// Expected to fail in test env without NATS.
}

func TestMessageSerialization(t *testing.T) {
	msg := Message{
		Topic:     TopicClaim,
		AgentID:   "agent-1",
		Payload:   []byte(`{"task":"demo"}`),
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data, err := msg.Payload.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"task":"demo"}` {
		t.Fatalf("unexpected payload: %s", string(data))
	}
}

func TestDispatcherTopicConstants(t *testing.T) {
	want := []string{TopicDispatch, TopicClaim, TopicResult, TopicHeartbeat, TopicComms}
	for _, topic := range want {
		if topic == "" {
			t.Fatal("topic constant is empty")
		}
	}
}
