// Package ops wraps side-effecting operations in idempotency envelopes.
package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Source struct {
	App     string `json:"app"`
	Version string `json:"version"`
}

type Target struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Retry struct {
	MaxAttempts    int `json:"max_attempts"`
	CurrentAttempt int `json:"current_attempt"`
}

type Envelope struct {
	OperationID    string    `json:"operation_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	Actor          Actor     `json:"actor"`
	Source         Source    `json:"source"`
	Target         Target    `json:"target"`
	Intent         string    `json:"intent"`
	PayloadHash    string    `json:"payload_hash"`
	CorrelationID  string    `json:"correlation_id"`
	TraceID        string    `json:"trace_id"`
	CreatedAt      time.Time `json:"created_at"`
	Retry          Retry     `json:"retry"`
}

type Store interface {
	HasOperation(ctx context.Context, key string) (bool, error)
	GetOperationResult(ctx context.Context, key string) (string, bool, error)
	RecordOperation(ctx context.Context, op Envelope, result string) error
}

type Broker struct {
	store Store
}

func NewBroker(store Store) *Broker { return &Broker{store: store} }

func (b *Broker) Do(ctx context.Context, op Envelope, fn func(context.Context) (string, error)) error {
	_, err := b.DoResult(ctx, op, fn)
	return err
}

func (b *Broker) DoResult(ctx context.Context, op Envelope, fn func(context.Context) (string, error)) (string, error) {
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
	if err := b.store.RecordOperation(ctx, op, result); err != nil {
		return "", fmt.Errorf("record operation: %w", err)
	}
	return result, nil
}

func NewKey(segments ...string) string {
	h := sha256.New()
	for _, segment := range segments {
		_, _ = h.Write([]byte(segment))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func NewEnvelope(actorType, actorID, app, version, targetKind, targetID, intent, payload string, attempt int) Envelope {
	now := time.Now().UTC()
	return Envelope{
		OperationID:    uuid.NewString(),
		IdempotencyKey: NewKey(targetKind, targetID, intent, payload),
		Actor:          Actor{Type: actorType, ID: actorID},
		Source:         Source{App: app, Version: version},
		Target:         Target{Kind: targetKind, ID: targetID},
		Intent:         intent,
		PayloadHash:    NewKey(payload),
		CorrelationID:  uuid.NewString(),
		TraceID:        uuid.NewString(),
		CreatedAt:      now,
		Retry:          Retry{MaxAttempts: 3, CurrentAttempt: attempt},
	}
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

type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(ctx context.Context, dsn string) (*PGStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	store := &PGStore{pool: pool}
	if err := store.Init(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (p *PGStore) Close() { p.pool.Close() }

func (p *PGStore) Init(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS coordination_operations (
	idempotency_key text PRIMARY KEY,
	envelope jsonb NOT NULL,
	result text NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now()
)`)
	return err
}

func (p *PGStore) HasOperation(ctx context.Context, key string) (bool, error) {
	var exists bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM coordination_operations WHERE idempotency_key=$1)`, key).Scan(&exists)
	return exists, err
}

func (p *PGStore) GetOperationResult(ctx context.Context, key string) (string, bool, error) {
	var result string
	err := p.pool.QueryRow(ctx, `SELECT result FROM coordination_operations WHERE idempotency_key=$1`, key).Scan(&result)
	if err != nil {
		return "", false, err
	}
	return result, true, nil
}

func (p *PGStore) RecordOperation(ctx context.Context, op Envelope, result string) error {
	data, err := json.Marshal(op)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `
INSERT INTO coordination_operations (idempotency_key, envelope, result)
VALUES ($1, $2, $3)
ON CONFLICT (idempotency_key) DO NOTHING`, op.IdempotencyKey, data, result)
	return err
}
