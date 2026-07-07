package election

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// mockEntry implements kvEntry for testing.
type mockEntry struct {
	value    []byte
	revision uint64
}

func (m *mockEntry) Value() []byte   { return m.value }
func (m *mockEntry) Revision() uint64 { return m.revision }

// mockKV implements kvStore for testing.
type mockKV struct {
	mu        sync.Mutex
	data      map[string]*mockEntry
	revisions map[string]uint64
}

func newMockKV() *mockKV {
	return &mockKV{
		data:      make(map[string]*mockEntry),
		revisions: make(map[string]uint64),
	}
}

func (m *mockKV) Create(key string, value []byte) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.data[key]; exists {
		return 0, nats.ErrKeyExists
	}
	m.revisions[key]++
	rev := m.revisions[key]
	m.data[key] = &mockEntry{value: value, revision: rev}
	return rev, nil
}

func (m *mockKV) Get(key string) (kvEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, exists := m.data[key]
	if !exists {
		return nil, fmt.Errorf("key not found")
	}
	return entry, nil
}

func (m *mockKV) Put(key string, value []byte) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revisions[key]++
	rev := m.revisions[key]
	m.data[key] = &mockEntry{value: value, revision: rev}
	return rev, nil
}

func (m *mockKV) Update(key string, value []byte, last uint64) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, exists := m.data[key]
	if !exists {
		return 0, fmt.Errorf("key not found")
	}
	if entry.revision != last {
		return 0, fmt.Errorf("revision mismatch")
	}
	m.revisions[key]++
	rev := m.revisions[key]
	m.data[key] = &mockEntry{value: value, revision: rev}
	return rev, nil
}

func (m *mockKV) Delete(key string, _ ...nats.DeleteOpt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	delete(m.revisions, key)
	return nil
}

// mockKVWithTTL wraps mockKV and auto-expires entries after a TTL.
type mockKVWithTTL struct {
	*mockKV
	ttl   time.Duration
	clock func() time.Time
}

func newMockKVWithTTL(ttl time.Duration) *mockKVWithTTL {
	return &mockKVWithTTL{
		mockKV: newMockKV(),
		ttl:    ttl,
		clock:  func() time.Time { return time.Now().UTC() },
	}
}

func (m *mockKVWithTTL) Get(key string) (kvEntry, error) {
	entry, err := m.mockKV.Get(key)
	if err != nil {
		return nil, err
	}
	var lease Lease
	if err := json.Unmarshal(entry.Value(), &lease); err == nil {
		if m.clock().After(lease.ExpiresAt) {
			_ = m.mockKV.Delete(key)
			return nil, fmt.Errorf("key expired")
		}
	}
	return entry, nil
}
