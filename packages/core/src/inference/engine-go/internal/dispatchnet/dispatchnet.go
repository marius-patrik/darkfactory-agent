// Package dispatchnet provides cross-agent communication over NATS.
package dispatchnet

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// Topic names for cross-agent communication.
const (
	TopicDispatch   = "dispatch"
	TopicClaim      = "claim"
	TopicResult     = "result"
	TopicHeartbeat  = "heartbeat"
	TopicComms      = "comms"
)

// Message is the envelope for all dispatchnet communications.
type Message struct {
	Topic     string          `json:"topic"`
	AgentID   string          `json:"agent_id"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
}

// Handler processes incoming messages on a topic.
type Handler func(ctx context.Context, msg Message) error

// Dispatcher is the cross-agent communication interface.
type Dispatcher interface {
	Publish(ctx context.Context, topic string, payload any) error
	Subscribe(ctx context.Context, topic string, handler Handler) error
	Close() error
}

// NATSDispatcher implements Dispatcher over NATS core pub/sub.
type NATSDispatcher struct {
	conn      *nats.Conn
	agentID   string
	namespace string
	log       *slog.Logger
	mu        sync.Mutex
	subs      map[string]*nats.Subscription
	closed    bool
}

// NewNATS creates a NATS-backed dispatcher.
func NewNATS(url, agentID, namespace string, log *slog.Logger) (*NATSDispatcher, error) {
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	return &NATSDispatcher{
		conn:      nc,
		agentID:   agentID,
		namespace: namespace,
		log:       log.With("component", "dispatchnet"),
		subs:      make(map[string]*nats.Subscription),
	}, nil
}

func (d *NATSDispatcher) subject(topic string) string {
	return fmt.Sprintf("%s.%s", d.namespace, topic)
}

// Publish sends a message to a topic.
func (d *NATSDispatcher) Publish(ctx context.Context, topic string, payload any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	msg := Message{
		Topic:     topic,
		AgentID:   d.agentID,
		Payload:   data,
		Timestamp: time.Now().UTC(),
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	subj := d.subject(topic)
	if err := d.conn.Publish(subj, body); err != nil {
		d.log.Warn("publish failed", "topic", topic, "err", err)
		return err
	}
	return nil
}

// Subscribe registers a handler for a topic.
func (d *NATSDispatcher) Subscribe(ctx context.Context, topic string, handler Handler) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	if _, exists := d.subs[topic]; exists {
		return fmt.Errorf("already subscribed to topic: %s", topic)
	}
	subj := d.subject(topic)
	sub, err := d.conn.Subscribe(subj, func(nmsg *nats.Msg) {
		var msg Message
		if err := json.Unmarshal(nmsg.Data, &msg); err != nil {
			d.log.Warn("dropping malformed message", "topic", topic, "err", err)
			return
		}
		if err := handler(ctx, msg); err != nil {
			d.log.Warn("handler error", "topic", topic, "err", err)
		}
	})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	d.subs[topic] = sub
	return nil
}

// Close unsubscribes all topics and closes the NATS connection.
func (d *NATSDispatcher) Close() error {
	d.mu.Lock()
	d.closed = true
	for _, sub := range d.subs {
		_ = sub.Unsubscribe()
	}
	d.mu.Unlock()
	d.conn.Close()
	return nil
}

// NoopDispatcher silently discards all operations.
type NoopDispatcher struct{}

// NewNoop creates a no-op dispatcher.
func NewNoop() *NoopDispatcher { return &NoopDispatcher{} }

// Publish is a no-op.
func (n *NoopDispatcher) Publish(ctx context.Context, topic string, payload any) error { return nil }

// Subscribe is a no-op.
func (n *NoopDispatcher) Subscribe(ctx context.Context, topic string, handler Handler) error { return nil }

// Close is a no-op.
func (n *NoopDispatcher) Close() error { return nil }
