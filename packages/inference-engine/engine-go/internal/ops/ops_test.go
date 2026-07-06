package ops

import (
	"context"
	"errors"
	"testing"

	"github.com/marius-patrik/agentos/inference-engine/engine-go/pkg/contracts"
)

type memStore struct {
	ops     map[string]contracts.OperationEnvelope
	results map[string]string
}

func newMemStore() *memStore {
	return &memStore{ops: make(map[string]contracts.OperationEnvelope), results: make(map[string]string)}
}

func (m *memStore) HasOperation(ctx context.Context, key string) (bool, error) {
	_, ok := m.ops[key]
	return ok, nil
}

func (m *memStore) RecordOperation(ctx context.Context, op contracts.OperationEnvelope, result string) error {
	m.ops[op.IdempotencyKey] = op
	m.results[op.IdempotencyKey] = result
	return nil
}

func (m *memStore) GetOperationResult(ctx context.Context, key string) (string, bool, error) {
	result, ok := m.results[key]
	return result, ok, nil
}

func TestBroker_Do_Idempotent(t *testing.T) {
	store := newMemStore()
	broker := NewBroker(store)
	ctx := context.Background()

	op := Envelope("system", "daemon", "engine", "0.1.0", "run", "r1", "test", "payload", 1)

	calls := 0
	fn := func(ctx context.Context) (string, error) {
		calls++
		return "ok", nil
	}

	if err := broker.Do(ctx, op, fn); err != nil {
		t.Fatalf("first do: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	// Second call with same idempotency key should not execute fn.
	if err := broker.Do(ctx, op, fn); err != nil {
		t.Fatalf("second do: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call after duplicate, got %d", calls)
	}
}

func TestBroker_DoResult_ReturnsStoredResult(t *testing.T) {
	store := newMemStore()
	broker := NewBroker(store)
	ctx := context.Background()

	op := Envelope("system", "daemon", "engine", "0.1.0", "run", "r1", "test", "payload", 1)

	calls := 0
	fn := func(ctx context.Context) (string, error) {
		calls++
		return "https://github.com/test/pr/1", nil
	}

	result, err := broker.DoResult(ctx, op, fn)
	if err != nil {
		t.Fatalf("first do result: %v", err)
	}
	if result != "https://github.com/test/pr/1" {
		t.Fatalf("first result = %q", result)
	}

	result, err = broker.DoResult(ctx, op, fn)
	if err != nil {
		t.Fatalf("second do result: %v", err)
	}
	if result != "https://github.com/test/pr/1" {
		t.Fatalf("stored result = %q", result)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestBroker_Do_DoesNotRecordFailedOperation(t *testing.T) {
	store := newMemStore()
	broker := NewBroker(store)
	ctx := context.Background()

	op := Envelope("system", "daemon", "engine", "0.1.0", "run", "r2", "fail", "payload", 1)
	calls := 0
	fn := func(ctx context.Context) (string, error) {
		calls++
		return "", errors.New("boom")
	}

	if err := broker.Do(ctx, op, fn); err == nil {
		t.Fatal("expected error")
	}

	if len(store.ops) != 0 {
		t.Fatalf("failed operation must remain retryable, got %d recorded operations", len(store.ops))
	}
	if err := broker.Do(ctx, op, fn); err == nil {
		t.Fatal("expected retry error")
	}
	if calls != 2 {
		t.Fatalf("expected failed operation to retry, got %d calls", calls)
	}
}

func TestNewKey_Deterministic(t *testing.T) {
	a := NewKey("a", "b", "c")
	b := NewKey("a", "b", "c")
	if a != b {
		t.Fatal("idempotency key not deterministic")
	}
}

