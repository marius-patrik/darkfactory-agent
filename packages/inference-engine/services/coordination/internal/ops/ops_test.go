package ops

import (
	"context"
	"testing"
)

func TestBrokerRedeliveryReturnsCachedResult(t *testing.T) {
	store := NewMemStore()
	broker := NewBroker(store)
	op := NewEnvelope("system", "daemon", "coordination", "test", "tool_call", "call-1", "execute", "payload", 1)
	calls := 0
	fn := func(context.Context) (string, error) {
		calls++
		return "ok", nil
	}
	got, err := broker.DoResult(context.Background(), op, fn)
	if err != nil || got != "ok" {
		t.Fatalf("first result = %q, err=%v", got, err)
	}
	got, err = broker.DoResult(context.Background(), op, fn)
	if err != nil || got != "ok" {
		t.Fatalf("cached result = %q, err=%v", got, err)
	}
	if calls != 1 {
		t.Fatalf("side effect ran %d times, want 1", calls)
	}
}

func TestBrokerDistinctKeysBothRun(t *testing.T) {
	store := NewMemStore()
	broker := NewBroker(store)
	calls := 0
	for _, id := range []string{"call-1", "call-2"} {
		op := NewEnvelope("system", "daemon", "coordination", "test", "tool_call", id, "execute", id, 1)
		if _, err := broker.DoResult(context.Background(), op, func(context.Context) (string, error) {
			calls++
			return id, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("side effects ran %d times, want 2", calls)
	}
}

func TestNewKeyDeterministic(t *testing.T) {
	if NewKey("a", "b") != NewKey("a", "b") {
		t.Fatal("key is not deterministic")
	}
	if NewKey("a", "b") == NewKey("ab") {
		t.Fatal("key segments are not delimited")
	}
}
