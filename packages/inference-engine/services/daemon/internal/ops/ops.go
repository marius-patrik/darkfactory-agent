// Package ops is the daemon-local idempotency broker for tool execution.
package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Envelope struct {
	OperationID    string    `json:"operation_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	TargetKind     string    `json:"target_kind"`
	TargetID       string    `json:"target_id"`
	Intent         string    `json:"intent"`
	PayloadHash    string    `json:"payload_hash"`
	CreatedAt      time.Time `json:"created_at"`
}

type Store interface {
	HasOperation(context.Context, string) (bool, error)
	GetOperationResult(context.Context, string) (string, bool, error)
	RecordOperation(context.Context, Envelope, string) error
}

type Broker struct {
	store Store
}

func NewBroker(store Store) *Broker { return &Broker{store: store} }

func (b *Broker) DoResult(ctx context.Context, op Envelope, fn func(context.Context) (string, error)) (string, error) {
	seen, err := b.store.HasOperation(ctx, op.IdempotencyKey)
	if err != nil {
		return "", err
	}
	if seen {
		result, ok, err := b.store.GetOperationResult(ctx, op.IdempotencyKey)
		if err != nil || !ok {
			return result, err
		}
		return result, nil
	}
	result, err := fn(ctx)
	if err != nil {
		return "", err
	}
	if err := b.store.RecordOperation(ctx, op, result); err != nil {
		return "", err
	}
	return result, nil
}

func NewEnvelope(targetKind, targetID, intent, payload string) Envelope {
	return Envelope{
		OperationID:    uuid.NewString(),
		IdempotencyKey: NewKey(targetKind, targetID, intent, payload),
		TargetKind:     targetKind,
		TargetID:       targetID,
		Intent:         intent,
		PayloadHash:    NewKey(payload),
		CreatedAt:      time.Now().UTC(),
	}
}

func NewKey(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

type MemStore struct {
	mu      sync.Mutex
	ops     map[string]Envelope
	results map[string]string
}

func NewMemStore() *MemStore {
	return &MemStore{ops: map[string]Envelope{}, results: map[string]string{}}
}

func (m *MemStore) HasOperation(ctx context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.ops[key]
	return ok, nil
}

func (m *MemStore) GetOperationResult(ctx context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result, ok := m.results[key]
	return result, ok, nil
}

func (m *MemStore) RecordOperation(ctx context.Context, op Envelope, result string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops[op.IdempotencyKey] = op
	m.results[op.IdempotencyKey] = result
	return nil
}
