package election

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

type mockEntry struct {
	value    []byte
	revision uint64
}

func (m mockEntry) Value() []byte    { return m.value }
func (m mockEntry) Revision() uint64 { return m.revision }

type mockKV struct {
	mu                  sync.Mutex
	data                map[string]mockEntry
	revisions           map[string]uint64
	failUpdate          bool
	deleteOnNextGet     bool
	createKeyExistsOnce bool
}

func newMockKV() *mockKV {
	return &mockKV{data: map[string]mockEntry{}, revisions: map[string]uint64{}}
}

func (m *mockKV) Create(key string, value []byte) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createKeyExistsOnce {
		m.createKeyExistsOnce = false
		return 0, nats.ErrKeyExists
	}
	if _, ok := m.data[key]; ok {
		return 0, nats.ErrKeyExists
	}
	m.revisions[key]++
	rev := m.revisions[key]
	m.data[key] = mockEntry{value: append([]byte(nil), value...), revision: rev}
	return rev, nil
}

func (m *mockKV) Get(key string) (kvEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteOnNextGet {
		m.deleteOnNextGet = false
		delete(m.data, key)
		return nil, errors.New("key not found")
	}
	entry, ok := m.data[key]
	if !ok {
		return nil, errors.New("key not found")
	}
	return entry, nil
}

func (m *mockKV) Put(key string, value []byte) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revisions[key]++
	rev := m.revisions[key]
	m.data[key] = mockEntry{value: append([]byte(nil), value...), revision: rev}
	return rev, nil
}

func (m *mockKV) Update(key string, value []byte, last uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failUpdate {
		return 0, errors.New("update failed")
	}
	entry, ok := m.data[key]
	if !ok {
		return 0, errors.New("key not found")
	}
	if entry.revision != last {
		return 0, errors.New("revision mismatch")
	}
	m.revisions[key]++
	rev := m.revisions[key]
	m.data[key] = mockEntry{value: append([]byte(nil), value...), revision: rev}
	return rev, nil
}

func (m *mockKV) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func testElection(nodeID string, kv *mockKV, now time.Time) *Election {
	e := newElection(nodeID, slog.New(slog.DiscardHandler), WithTTL(time.Minute), WithRetryDelay(time.Millisecond))
	e.kv = kv
	e.now = func() time.Time { return now }
	return e
}

func seedLease(t *testing.T, kv *mockKV, key string, lease Lease) {
	t.Helper()
	data, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(key, data); err != nil {
		t.Fatal(err)
	}
}

func TestLiveOtherNodeLeaseIsNotLeader(t *testing.T) {
	now := time.Now().UTC()
	kv := newMockKV()
	seedLease(t, kv, defaultKey, Lease{NodeID: "node-a", Acquired: now, ExpiresAt: now.Add(time.Hour)})
	e := testElection("node-b", kv, now)
	if err := e.tryAcquire(context.Background()); err == nil {
		t.Fatal("expected valid foreign lease to block acquisition")
	}
	if e.IsLeader() {
		t.Fatal("fail-closed election must not report leader")
	}
}

func TestExpiredLeaseIsStolenWithCAS(t *testing.T) {
	now := time.Now().UTC()
	kv := newMockKV()
	seedLease(t, kv, defaultKey, Lease{NodeID: "node-a", Acquired: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)})
	e := testElection("node-b", kv, now)
	e.tick(context.Background())
	if !e.IsLeader() {
		t.Fatal("expected tick to become leader after CAS steal")
	}
}

func TestRenewalFailureOrStolenLeaseStepsDown(t *testing.T) {
	now := time.Now().UTC()
	kv := newMockKV()
	seedLease(t, kv, defaultKey, Lease{NodeID: "node-a", Acquired: now, ExpiresAt: now.Add(time.Hour)})
	e := testElection("node-a", kv, now)
	e.setLeader(true)
	kv.failUpdate = true
	e.tick(context.Background())
	if e.IsLeader() {
		t.Fatal("expected tick to step down after renewal failure")
	}

	kv.failUpdate = false
	seedLease(t, kv, defaultKey, Lease{NodeID: "node-b", Acquired: now, ExpiresAt: now.Add(time.Hour)})
	e.setLeader(true)
	e.tick(context.Background())
	if e.IsLeader() {
		t.Fatal("expected tick to step down after stolen lease")
	}
}

func TestTickBecomesLeaderWhenLeaseIsFree(t *testing.T) {
	now := time.Now().UTC()
	kv := newMockKV()
	e := testElection("node-a", kv, now)
	e.tick(context.Background())
	if !e.IsLeader() {
		t.Fatal("expected tick to become leader when lease is free")
	}
}

func TestTickSuccessfulRenewStaysLeader(t *testing.T) {
	now := time.Now().UTC()
	kv := newMockKV()
	seedLease(t, kv, defaultKey, Lease{NodeID: "node-a", Acquired: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)})
	e := testElection("node-a", kv, now)
	e.setLeader(true)
	e.tick(context.Background())
	if !e.IsLeader() {
		t.Fatal("expected tick to stay leader after successful renew")
	}
}

func TestRunCallsTickAndClearsLeaderOnCancel(t *testing.T) {
	now := time.Now().UTC()
	kv := newMockKV()
	e := testElection("node-a", kv, now)
	e.retryDelay = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- e.Run(ctx)
	}()

	deadline := time.After(time.Second)
	for !e.IsLeader() {
		select {
		case err := <-done:
			t.Fatalf("Run returned before leadership acquisition: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for Run to acquire leadership")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run to exit")
	}
	if e.IsLeader() {
		t.Fatal("expected Run to clear leadership on cancellation")
	}
}

func TestKeyDeletedBetweenCreateAndGetIsHandled(t *testing.T) {
	now := time.Now().UTC()
	kv := newMockKV()
	kv.createKeyExistsOnce = true
	kv.deleteOnNextGet = true
	e := testElection("node-a", kv, now)
	if err := e.tryAcquire(context.Background()); err != nil {
		t.Fatalf("second create should acquire after deletion race: %v", err)
	}
}

func TestCorruptedLeaseEntryIsOverwritten(t *testing.T) {
	now := time.Now().UTC()
	kv := newMockKV()
	if _, err := kv.Put(defaultKey, []byte("not-json")); err != nil {
		t.Fatal(err)
	}
	e := testElection("node-a", kv, now)
	if err := e.tryAcquire(context.Background()); err != nil {
		t.Fatalf("overwrite corrupt lease: %v", err)
	}
	entry, err := kv.Get(defaultKey)
	if err != nil {
		t.Fatal(err)
	}
	var lease Lease
	if err := json.Unmarshal(entry.Value(), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.NodeID != "node-a" {
		t.Fatalf("lease node = %q", lease.NodeID)
	}
}
