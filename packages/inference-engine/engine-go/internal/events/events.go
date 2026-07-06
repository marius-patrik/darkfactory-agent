// Package events provides an EventBus abstraction with NATS and no-op fallback.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

// Bus is the event bus interface.
type Bus interface {
	PublishRunEvent(ctx context.Context, ev contracts.RunEvent) error
	Close() error
}

// NATSBus publishes events to NATS JetStream.
type NATSBus struct {
	conn   *nats.Conn
	js     nats.JetStreamContext
	topic  string
	log    *slog.Logger
	mu     sync.Mutex
	closed bool
}

// NewNATS connects to NATS and creates a JetStream context.
func NewNATS(url, topic string, log *slog.Logger) (*NATSBus, error) {
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats jetstream: %w", err)
	}
	streamConfig := &nats.StreamConfig{
		Name:     "AGENTS_RUNS",
		Subjects: runEventStreamSubjects(topic),
		MaxMsgs:  100_000,
	}
	// Ensure stream exists and covers the configured topic. A stale or invalid
	// subject here makes required queue event delivery fail at publish time.
	if _, err := js.AddStream(streamConfig); err != nil {
		if _, updateErr := js.UpdateStream(streamConfig); updateErr != nil {
			nc.Close()
			return nil, fmt.Errorf("nats stream AGENTS_RUNS: add: %w; update: %v", err, updateErr)
		}
	}
	return &NATSBus{
		conn:  nc,
		js:    js,
		topic: topic,
		log:   log.With("component", "events"),
	}, nil
}

func runEventStreamSubjects(topic string) []string {
	return []string{topic + ".>"}
}

// PublishRunEvent publishes a run event.
func (b *NATSBus) PublishRunEvent(ctx context.Context, ev contracts.RunEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	subj := fmt.Sprintf("%s.%s.%s", b.topic, ev.RunID, ev.To)
	_, err = b.js.Publish(subj, data)
	if err != nil {
		b.log.Warn("nats publish failed", "err", err)
		return err
	}
	return nil
}

// Close closes the NATS connection.
func (b *NATSBus) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.conn.Close()
	return nil
}

// NoopBus silently discards events.
type NoopBus struct{}

// NewNoop creates a no-op bus.
func NewNoop() *NoopBus { return &NoopBus{} }

// PublishRunEvent is a no-op.
func (n *NoopBus) PublishRunEvent(ctx context.Context, ev contracts.RunEvent) error { return nil }

// Close is a no-op.
func (n *NoopBus) Close() error { return nil }

