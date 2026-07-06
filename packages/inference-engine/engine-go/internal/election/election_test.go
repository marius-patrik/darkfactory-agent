package election

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestNoopElection(t *testing.T) {
	e := NewNoop("node-a")
	if !e.IsLeader() {
		t.Fatal("noop should always be leader")
	}
	if e.NodeID() != "node-a" {
		t.Fatalf("unexpected node id: %s", e.NodeID())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()

	select {
	case err := <-done:
		if err != context.DeadlineExceeded && err != context.Canceled {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after context cancelled")
	}
}

func TestNATSElection_AcquireAndRenew(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Use a mock KV with a 500ms TTL to test expiry later.
	kv := newMockKVWithTTL(500 * time.Millisecond)
	e := &NATSElection{
		nodeID:     "node-a",
		bucket:     "test",
		key:        "leader",
		ttl:        500 * time.Millisecond,
		renewal:    100 * time.Millisecond,
		retryDelay: 50 * time.Millisecond,
		log:        log.With("component", "election"),
		kv:         kv,
		stopCh:     make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.Run(ctx)
	defer e.Close()

	// Wait for acquisition.
	time.Sleep(150 * time.Millisecond)
	if !e.IsLeader() {
		t.Fatal("node-a should have acquired lease")
	}

	// Let it renew a few times.
	time.Sleep(300 * time.Millisecond)
	if !e.IsLeader() {
		t.Fatal("node-a should still be leader after renewals")
	}
}

func TestNATSElection_ExpiryAndFailover(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Short TTL so expiry happens quickly.
	kv := newMockKVWithTTL(200 * time.Millisecond)

	leader := &NATSElection{
		nodeID:     "node-a",
		bucket:     "test",
		key:        "leader",
		ttl:        200 * time.Millisecond,
		renewal:    50 * time.Millisecond,
		retryDelay: 50 * time.Millisecond,
		log:        log.With("component", "election"),
		kv:         kv,
		stopCh:     make(chan struct{}),
	}

	standby := &NATSElection{
		nodeID:     "node-b",
		bucket:     "test",
		key:        "leader",
		ttl:        200 * time.Millisecond,
		renewal:    50 * time.Millisecond,
		retryDelay: 50 * time.Millisecond,
		log:        log.With("component", "election"),
		kv:         kv,
		stopCh:     make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go leader.Run(ctx)
	defer leader.Close()

	// Wait for leader to acquire before starting standby.
	time.Sleep(150 * time.Millisecond)
	if !leader.IsLeader() {
		t.Fatal("node-a should be leader")
	}

	go standby.Run(ctx)
	defer standby.Close()

	if standby.IsLeader() {
		t.Fatal("node-b should be standby")
	}

	// Simulate leader death: stop renewing.
	leader.Close()

	// Wait for TTL to expire and standby to take over.
	time.Sleep(400 * time.Millisecond)
	if !standby.IsLeader() {
		t.Fatal("node-b should have taken over after node-a died")
	}
}

func TestNATSElection_Release(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	kv := newMockKVWithTTL(1 * time.Second)
	e := &NATSElection{
		nodeID:     "node-a",
		bucket:     "test",
		key:        "leader",
		ttl:        1 * time.Second,
		renewal:    100 * time.Millisecond,
		retryDelay: 50 * time.Millisecond,
		log:        log.With("component", "election"),
		kv:         kv,
		stopCh:     make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.Run(ctx)
	defer e.Close()

	time.Sleep(100 * time.Millisecond)
	if !e.IsLeader() {
		t.Fatal("should be leader")
	}

	if err := e.Release(ctx); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	if e.IsLeader() {
		t.Fatal("should have stepped down after release")
	}

	// Verify key is deleted.
	_, err := kv.Get("leader")
	if err == nil {
		t.Fatal("lease key should have been deleted")
	}
}

func TestNATSElection_CASSteal(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Fixed clock so we control expiry manually.
	now := time.Now().UTC()
	kv := newMockKVWithTTL(1 * time.Hour)
	kv.clock = func() time.Time { return now }

	// Seed an expired lease held by node-a.
	expiredLease := Lease{
		NodeID:    "node-a",
		Acquired:  now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(-1 * time.Hour),
	}
	data, _ := json.Marshal(expiredLease)
	_, _ = kv.Put("leader", data)

	e := &NATSElection{
		nodeID:     "node-b",
		bucket:     "test",
		key:        "leader",
		ttl:        1 * time.Hour,
		renewal:    100 * time.Millisecond,
		retryDelay: 50 * time.Millisecond,
		log:        log.With("component", "election"),
		kv:         kv,
		stopCh:     make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.Run(ctx)
	defer e.Close()

	time.Sleep(100 * time.Millisecond)
	if !e.IsLeader() {
		t.Fatal("node-b should have stolen expired lease from node-a")
	}
}

func TestNATSElection_StolenLease(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	now := time.Now().UTC()
	kv := newMockKVWithTTL(1 * time.Hour)
	kv.clock = func() time.Time { return now }

	// Seed a valid lease held by another node.
	otherLease := Lease{
		NodeID:    "node-a",
		Acquired:  now,
		ExpiresAt: now.Add(1 * time.Hour),
	}
	data, _ := json.Marshal(otherLease)
	_, _ = kv.Put("leader", data)

	e := &NATSElection{
		nodeID:     "node-b",
		bucket:     "test",
		key:        "leader",
		ttl:        1 * time.Hour,
		renewal:    100 * time.Millisecond,
		retryDelay: 50 * time.Millisecond,
		log:        log.With("component", "election"),
		kv:         kv,
		stopCh:     make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.Run(ctx)
	defer e.Close()

	time.Sleep(100 * time.Millisecond)
	if e.IsLeader() {
		t.Fatal("node-b should NOT steal a valid lease")
	}
}

func TestNATSElection_RenewDetectsTheft(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	now := time.Now().UTC()
	kv := newMockKVWithTTL(1 * time.Hour)
	kv.clock = func() time.Time { return now }

	e := &NATSElection{
		nodeID:     "node-a",
		bucket:     "test",
		key:        "leader",
		ttl:        1 * time.Hour,
		renewal:    100 * time.Millisecond,
		retryDelay: 50 * time.Millisecond,
		log:        log.With("component", "election"),
		kv:         kv,
		stopCh:     make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go e.Run(ctx)
	defer e.Close()

	time.Sleep(100 * time.Millisecond)
	if !e.IsLeader() {
		t.Fatal("node-a should be leader")
	}

	// Another node steals the lease by direct Put.
	stolenLease := Lease{
		NodeID:    "node-b",
		Acquired:  now,
		ExpiresAt: now.Add(1 * time.Hour),
	}
	data, _ := json.Marshal(stolenLease)
	_, _ = kv.Put("leader", data)

	// Wait for renewal cycle to detect theft.
	time.Sleep(150 * time.Millisecond)
	if e.IsLeader() {
		t.Fatal("node-a should have stepped down after lease was stolen")
	}
}

func TestNATSElection_NATSConnection(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := NewNATS("nats://localhost:4222", "test-node", log)
	if err == nil {
		t.Skip("nats unexpectedly available")
	}
	// Expected to fail in test env without NATS.
}
