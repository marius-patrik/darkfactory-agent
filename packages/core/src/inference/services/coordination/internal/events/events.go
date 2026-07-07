// Package events publishes and subscribes to run and job lifecycle events.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const DefaultSubjectPrefix = "rommie.events"

type Event struct {
	Type      string          `json:"type"`
	RunID     string          `json:"run_id,omitempty"`
	JobID     string          `json:"job_id,omitempty"`
	Status    string          `json:"status,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type Bus struct {
	conn   *nats.Conn
	js     nats.JetStreamContext
	prefix string
	log    *slog.Logger
	mu     sync.Mutex
	closed bool
}

func NewNATS(url, prefix string, log *slog.Logger) (*Bus, error) {
	if prefix == "" {
		prefix = DefaultSubjectPrefix
	}
	if log == nil {
		log = slog.Default()
	}
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats jetstream: %w", err)
	}
	cfg := &nats.StreamConfig{Name: "ROMMIE_EVENTS", Subjects: []string{prefix + ".>"}}
	if _, err := js.AddStream(cfg); err != nil {
		if _, updateErr := js.UpdateStream(cfg); updateErr != nil {
			nc.Close()
			return nil, fmt.Errorf("nats stream ROMMIE_EVENTS: add: %w; update: %v", err, updateErr)
		}
	}
	return &Bus{conn: nc, js: js, prefix: prefix, log: log.With("component", "events")}, nil
}

func (b *Bus) Publish(ctx context.Context, ev Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	subj := b.subject(ev)
	_, err = b.js.Publish(subj, data, nats.Context(ctx))
	if err != nil {
		b.log.Warn("event publish failed", "subject", subj, "err", err)
	}
	return err
}

func (b *Bus) Subscribe(ctx context.Context, subject string, handler func(context.Context, Event) error) (*nats.Subscription, error) {
	if subject == "" {
		subject = b.prefix + ".>"
	}
	return b.js.Subscribe(subject, func(msg *nats.Msg) {
		var ev Event
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			_ = msg.Nak()
			return
		}
		if err := handler(ctx, ev); err != nil {
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	}, nats.ManualAck())
}

func (b *Bus) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	if b.conn != nil {
		b.conn.Close()
	}
	return nil
}

func (b *Bus) subject(ev Event) string {
	kind := "event"
	id := ev.RunID
	if ev.JobID != "" {
		kind = "job"
		id = ev.JobID
	} else if ev.RunID != "" {
		kind = "run"
	}
	if id == "" {
		id = "_"
	}
	status := ev.Status
	if status == "" {
		status = ev.Type
	}
	if status == "" {
		status = "unknown"
	}
	return fmt.Sprintf("%s.%s.%s.%s", b.prefix, kind, id, status)
}

type Noop struct{}

func NewNoop() *Noop { return &Noop{} }
func (n *Noop) Publish(ctx context.Context, ev Event) error {
	return nil
}
func (n *Noop) Close() error { return nil }
