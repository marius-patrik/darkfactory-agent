package events

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

func TestNoopBus(t *testing.T) {
	bus := NewNoop()
	ctx := context.Background()
	ev := contracts.RunEvent{
		TraceID:   "t1",
		RunID:     "r1",
		From:      contracts.RunStatusQueued,
		To:        contracts.RunStatusRunning,
		Timestamp: time.Now().UTC(),
	}
	if err := bus.PublishRunEvent(ctx, ev); err != nil {
		t.Fatalf("noop publish: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("noop close: %v", err)
	}
}

func TestRunEventStreamSubjectsMatchPublishedRunEvents(t *testing.T) {
	subjects := runEventStreamSubjects("agents.runs")
	if len(subjects) != 1 || subjects[0] != "agents.runs.>" {
		t.Fatalf("subjects = %#v, want agents.runs.>", subjects)
	}
	if subjects[0] == "agents.runs.*>" {
		t.Fatalf("invalid NATS wildcard subject was restored: %q", subjects[0])
	}
}

func TestNATSBus_Connect(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := NewNATS("nats://localhost:4222", "agents.runs", log)
	if err == nil {
		t.Skip("nats unexpectedly available")
	}
	// Expected to fail in test env without NATS.
}

