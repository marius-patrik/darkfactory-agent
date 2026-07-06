// Package election provides leader election over NATS Key-Value store.
package election

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	defaultBucket     = "AGENTS_LEADER"
	defaultKey        = "leader"
	defaultTTL        = 30 * time.Second
	defaultRenewal    = 10 * time.Second
	defaultRetryDelay = 5 * time.Second
)

// Lease is the value stored in the KV bucket.
type Lease struct {
	NodeID    string    `json:"node_id"`
	Acquired  time.Time `json:"acquired"`
	ExpiresAt time.Time `json:"expires_at"`
}

// kvStore is the minimal KeyValue interface used by election.
type kvStore interface {
	Create(key string, value []byte) (uint64, error)
	Get(key string) (kvEntry, error)
	Put(key string, value []byte) (uint64, error)
	Update(key string, value []byte, last uint64) (uint64, error)
	Delete(key string, opts ...nats.DeleteOpt) error
}

// kvEntry is the minimal entry interface used by election.
type kvEntry interface {
	Value() []byte
	Revision() uint64
}

// Election manages leader election state.
type Election interface {
	Run(ctx context.Context) error
	IsLeader() bool
	NodeID() string
}

// NATSElection implements Election using NATS Key-Value.
type NATSElection struct {
	nodeID     string
	bucket     string
	key        string
	ttl        time.Duration
	renewal    time.Duration
	retryDelay time.Duration

	kv   kvStore
	nc   *nats.Conn
	log  *slog.Logger

	mu        sync.RWMutex
	leader    bool
	stopCh    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// Option configures NATSElection.
type Option func(*NATSElection)

// WithBucket sets the KV bucket name.
func WithBucket(name string) Option { return func(e *NATSElection) { e.bucket = name } }

// WithKey sets the KV key name.
func WithKey(key string) Option { return func(e *NATSElection) { e.key = key } }

// WithTTL sets the lease TTL.
func WithTTL(d time.Duration) Option { return func(e *NATSElection) { e.ttl = d } }

// WithRenewal sets the renewal interval.
func WithRenewal(d time.Duration) Option { return func(e *NATSElection) { e.renewal = d } }

// NewNATS creates a NATS-backed election.
func NewNATS(url, nodeID string, log *slog.Logger, opts ...Option) (*NATSElection, error) {
	e := &NATSElection{
		nodeID:     nodeID,
		bucket:     defaultBucket,
		key:        defaultKey,
		ttl:        defaultTTL,
		renewal:    defaultRenewal,
		retryDelay: defaultRetryDelay,
		log:        log.With("component", "election", "node_id", nodeID),
		stopCh:     make(chan struct{}),
	}
	for _, o := range opts {
		o(e)
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
	e.nc = nc

	// Ensure NATS KV bucket uses the TTL for auto-expiry.
	nkv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket: e.bucket,
		TTL:    e.ttl,
	})
	if err != nil {
		// Bucket may already exist.
		nkv, err = js.KeyValue(e.bucket)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("nats kv: %w", err)
		}
	}
	e.kv = &natsKVWrapper{kv: nkv}
	return e, nil
}

// natsKVWrapper adapts nats.KeyValue to kvStore.
type natsKVWrapper struct {
	kv nats.KeyValue
}

func (w *natsKVWrapper) Create(key string, value []byte) (uint64, error) {
	return w.kv.Create(key, value)
}

func (w *natsKVWrapper) Get(key string) (kvEntry, error) {
	return w.kv.Get(key)
}

func (w *natsKVWrapper) Put(key string, value []byte) (uint64, error) {
	return w.kv.Put(key, value)
}

func (w *natsKVWrapper) Update(key string, value []byte, last uint64) (uint64, error) {
	return w.kv.Update(key, value, last)
}

func (w *natsKVWrapper) Delete(key string, _ ...nats.DeleteOpt) error {
	return w.kv.Delete(key)
}

// Run starts the election loop. It blocks until ctx is cancelled.
func (e *NATSElection) Run(ctx context.Context) error {
	e.wg.Add(1)
	defer e.wg.Done()

	ticker := time.NewTicker(e.retryDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.stopCh:
			return nil
		case <-ticker.C:
		}

		if e.IsLeader() {
			if err := e.renew(ctx); err != nil {
				e.log.Warn("lease renewal failed, stepping down", "err", err)
				e.setLeader(false)
			}
		} else {
			if err := e.tryAcquire(ctx); err == nil {
				e.log.Info("lease acquired, becoming leader")
				e.setLeader(true)
			}
		}
	}
}

// IsLeader reports whether this node currently holds the leader lease.
func (e *NATSElection) IsLeader() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leader
}

// NodeID returns the node identity.
func (e *NATSElection) NodeID() string { return e.nodeID }

// Release explicitly releases the leader lease if held.
func (e *NATSElection) Release(ctx context.Context) error {
	e.setLeader(false)
	entry, err := e.kv.Get(e.key)
	if err != nil {
		return nil // already gone
	}
	var lease Lease
	if err := json.Unmarshal(entry.Value(), &lease); err != nil {
		return fmt.Errorf("unmarshal lease: %w", err)
	}
	if lease.NodeID == e.nodeID {
		_ = e.kv.Delete(e.key)
		e.log.Info("lease released")
	}
	return nil
}

// Close shuts down the election and releases the NATS connection.
func (e *NATSElection) Close() error {
	e.closeOnce.Do(func() {
		close(e.stopCh)
	})
	e.wg.Wait()
	if e.nc != nil {
		e.nc.Close()
	}
	return nil
}

func (e *NATSElection) setLeader(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.leader != v {
		e.leader = v
		e.log.Info("leadership changed", "leader", v)
	}
}

func (e *NATSElection) tryAcquire(ctx context.Context) error {
	now := time.Now().UTC()
	lease := Lease{
		NodeID:    e.nodeID,
		Acquired:  now,
		ExpiresAt: now.Add(e.ttl),
	}
	data, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("marshal lease: %w", err)
	}

	// Try to create the key. If it already exists, check if it expired.
	_, err = e.kv.Create(e.key, data)
	if err == nil {
		return nil // acquired
	}

	if err != nats.ErrKeyExists {
		return fmt.Errorf("kv create: %w", err)
	}

	// Key exists — check if current lease expired.
	entry, err := e.kv.Get(e.key)
	if err != nil {
		// Key may have been deleted between Create and Get.
		_, err2 := e.kv.Create(e.key, data)
		return err2
	}

	var current Lease
	if err := json.Unmarshal(entry.Value(), &current); err != nil {
		// Corrupted entry — overwrite.
		_, err = e.kv.Put(e.key, data)
		return err
	}

	if time.Now().UTC().After(current.ExpiresAt) {
		// Expired — try CAS update to steal.
		_, err = e.kv.Update(e.key, data, entry.Revision())
		return err
	}

	return fmt.Errorf("lease held by %s until %s", current.NodeID, current.ExpiresAt.Format(time.RFC3339))
}

func (e *NATSElection) renew(ctx context.Context) error {
	entry, err := e.kv.Get(e.key)
	if err != nil {
		return fmt.Errorf("kv get: %w", err)
	}

	var current Lease
	if err := json.Unmarshal(entry.Value(), &current); err != nil {
		return fmt.Errorf("unmarshal lease: %w", err)
	}

	if current.NodeID != e.nodeID {
		return fmt.Errorf("lease stolen by %s", current.NodeID)
	}

	now := time.Now().UTC()
	lease := Lease{
		NodeID:    e.nodeID,
		Acquired:  current.Acquired,
		ExpiresAt: now.Add(e.ttl),
	}
	data, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("marshal lease: %w", err)
	}

	_, err = e.kv.Update(e.key, data, entry.Revision())
	if err != nil {
		return fmt.Errorf("kv update: %w", err)
	}
	return nil
}
