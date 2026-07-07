// Package ops wraps side-effecting work with OperationEnvelope idempotency.
package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

// Store is the subset of store.Store needed by ops.
type Store interface {
	HasOperation(ctx context.Context, key string) (bool, error)
	GetOperationResult(ctx context.Context, key string) (string, bool, error)
	RecordOperation(ctx context.Context, op contracts.OperationEnvelope, result string) error
}

// Broker executes idempotent operations.
type Broker struct {
	store Store
}

// NewBroker creates a Broker.
func NewBroker(store Store) *Broker {
	return &Broker{store: store}
}

// Do executes fn if the idempotency key has not been seen.
func (b *Broker) Do(ctx context.Context, op contracts.OperationEnvelope, fn func(ctx context.Context) (string, error)) error {
	_, err := b.DoResult(ctx, op, fn)
	return err
}

// DoResult executes fn if the idempotency key has not been seen; otherwise returns the previous result.
func (b *Broker) DoResult(ctx context.Context, op contracts.OperationEnvelope, fn func(ctx context.Context) (string, error)) (string, error) {
	seen, err := b.store.HasOperation(ctx, op.IdempotencyKey)
	if err != nil {
		return "", fmt.Errorf("idempotency check: %w", err)
	}
	if seen {
		result, ok, err := b.store.GetOperationResult(ctx, op.IdempotencyKey)
		if err != nil {
			return "", fmt.Errorf("load operation result: %w", err)
		}
		if !ok {
			return "", nil
		}
		return result, nil
	}
	result, err := fn(ctx)
	if err != nil {
		return "", err
	}
	if rerr := b.store.RecordOperation(ctx, op, result); rerr != nil {
		return "", fmt.Errorf("record operation: %w", rerr)
	}
	return result, err
}

// NewKey builds a deterministic idempotency key from segments.
func NewKey(segments ...string) string {
	h := sha256.New()
	for _, s := range segments {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte("\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// Envelope builds a standard OperationEnvelope.
func Envelope(actorType, actorID, app, version, targetKind, targetID, intent, payload string, attempt int) contracts.OperationEnvelope {
	now := time.Now().UTC()
	return contracts.OperationEnvelope{
		OperationID:    uuid.Must(uuid.NewV7()).String(),
		IdempotencyKey: NewKey(targetKind, targetID, intent, payload),
		Actor:          contracts.Actor{Type: actorType, ID: actorID},
		Source:         contracts.Source{App: app, Version: version},
		Target:         contracts.Target{Kind: targetKind, ID: targetID},
		Intent:         intent,
		PayloadHash:    NewKey(payload),
		CorrelationID:  uuid.Must(uuid.NewV7()).String(),
		TraceID:        uuid.Must(uuid.NewV7()).String(),
		CreatedAt:      now,
		Retry:          contracts.Retry{MaxAttempts: 3, CurrentAttempt: attempt},
	}
}

