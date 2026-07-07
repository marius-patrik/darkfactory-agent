// Package election provides fail-closed leader election over NATS KV.
package election

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	defaultBucket     = "ROMMIE_LEADER"
	defaultKey        = "leader"
	defaultTTL        = 30 * time.Second
	defaultRenewal    = 10 * time.Second
	defaultRetryDelay = 5 * time.Second
)

// Lease is the JSON value stored in the KV bucket.
type Lease struct {
	NodeID    string    `json:"node_id"`
	Acquired  time.Time `json:"acquired"`
	ExpiresAt time.Time `json:"expires_at"`
}

type kvStore interface {
	Create(key string, value []byte) (uint64, error)
	Get(key string) (kvEntry, error)
	Put(key string, value []byte) (uint64, error)
	Update(key string, value []byte, last uint64) (uint64, error)
	Delete(key string) error
}

type kvEntry interface {
	Value() []byte
	Revision() uint64
}

// Election is a leader-election participant.
type Election struct {
	nodeID     string
	bucket     string
	key        string
	ttl        time.Duration
	renewal    time.Duration
	retryDelay time.Duration
	now        func() time.Time

	kv  kvStore
	nc  *nats.Conn
	log *slog.Logger

	mu     sync.RWMutex
	leader bool
}

// Option configures an Election.
type Option func(*Election)

func WithBucket(bucket string) Option { return func(e *Election) { e.bucket = bucket } }
func WithKey(key string) Option       { return func(e *Election) { e.key = key } }
func WithTTL(ttl time.Duration) Option {
	return func(e *Election) {
		if ttl > 0 {
			e.ttl = ttl
		}
	}
}
func WithRenewal(renewal time.Duration) Option {
	return func(e *Election) {
		if renewal > 0 {
			e.renewal = renewal
		}
	}
}
func WithRetryDelay(delay time.Duration) Option {
	return func(e *Election) {
		if delay > 0 {
			e.retryDelay = delay
		}
	}
}

// NewNATS creates a NATS KV backed election.
func NewNATS(url, nodeID string, log *slog.Logger, opts ...Option) (*Election, error) {
	e := newElection(nodeID, log, opts...)
	nc, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats jetstream: %w", err)
	}
	nkv, err := js.CreateKeyValue(&nats.KeyValueConfig{Bucket: e.bucket, TTL: e.ttl})
	if err != nil {
		nkv, err = js.KeyValue(e.bucket)
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("nats kv: %w", err)
		}
	}
	e.nc = nc
	e.kv = natsKV{kv: nkv}
	return e, nil
}

func newElection(nodeID string, log *slog.Logger, opts ...Option) *Election {
	if log == nil {
		log = slog.Default()
	}
	e := &Election{
		nodeID:     nodeID,
		bucket:     defaultBucket,
		key:        defaultKey,
		ttl:        defaultTTL,
		renewal:    defaultRenewal,
		retryDelay: defaultRetryDelay,
		now:        func() time.Time { return time.Now().UTC() },
		log:        log.With("component", "election", "node_id", nodeID),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

type natsKV struct {
	kv nats.KeyValue
}

func (n natsKV) Create(key string, value []byte) (uint64, error) { return n.kv.Create(key, value) }
func (n natsKV) Get(key string) (kvEntry, error)                 { return n.kv.Get(key) }
func (n natsKV) Put(key string, value []byte) (uint64, error)    { return n.kv.Put(key, value) }
func (n natsKV) Update(key string, value []byte, last uint64) (uint64, error) {
	return n.kv.Update(key, value, last)
}
func (n natsKV) Delete(key string) error { return n.kv.Delete(key) }

// Run loops until ctx is canceled. IsLeader is false unless this node provably owns the lease.
func (e *Election) Run(ctx context.Context) error {
	ticker := time.NewTicker(e.retryDelay)
	defer ticker.Stop()
	for {
		e.tick(ctx)

		select {
		case <-ctx.Done():
			e.setLeader(false)
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (e *Election) tick(ctx context.Context) {
	if e.IsLeader() {
		if err := e.renew(ctx); err != nil {
			e.log.Warn("lease renewal failed, stepping down", "err", err)
			e.setLeader(false)
		}
	} else if err := e.tryAcquire(ctx); err == nil {
		e.setLeader(true)
	} else {
		e.setLeader(false)
	}
}

func (e *Election) IsLeader() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leader
}

func (e *Election) NodeID() string { return e.nodeID }

func (e *Election) Release(ctx context.Context) error {
	e.setLeader(false)
	if e.kv == nil {
		return nil
	}
	entry, err := e.kv.Get(e.key)
	if err != nil {
		return nil
	}
	var lease Lease
	if err := json.Unmarshal(entry.Value(), &lease); err != nil {
		return fmt.Errorf("unmarshal lease: %w", err)
	}
	if lease.NodeID == e.nodeID {
		_ = e.kv.Delete(e.key)
	}
	return nil
}

func (e *Election) Close() error {
	if e.nc != nil {
		e.nc.Close()
	}
	e.setLeader(false)
	return nil
}

func (e *Election) setLeader(v bool) {
	e.mu.Lock()
	changed := e.leader != v
	e.leader = v
	e.mu.Unlock()
	if changed {
		e.log.Info("leadership changed", "leader", v)
	}
}

func (e *Election) tryAcquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(e.newLease(e.now()))
	if err != nil {
		return fmt.Errorf("marshal lease: %w", err)
	}
	if _, err := e.kv.Create(e.key, data); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrKeyExists) {
		return fmt.Errorf("kv create: %w", err)
	}

	entry, err := e.kv.Get(e.key)
	if err != nil {
		_, err2 := e.kv.Create(e.key, data)
		return err2
	}
	var current Lease
	if err := json.Unmarshal(entry.Value(), &current); err != nil {
		_, err = e.kv.Put(e.key, data)
		return err
	}
	if !e.now().Before(current.ExpiresAt) {
		_, err = e.kv.Update(e.key, data, entry.Revision())
		return err
	}
	return fmt.Errorf("lease held by %s until %s", current.NodeID, current.ExpiresAt.Format(time.RFC3339Nano))
}

func (e *Election) renew(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	lease := e.newLease(e.now())
	lease.Acquired = current.Acquired
	data, err := json.Marshal(lease)
	if err != nil {
		return fmt.Errorf("marshal lease: %w", err)
	}
	if _, err := e.kv.Update(e.key, data, entry.Revision()); err != nil {
		return fmt.Errorf("kv update: %w", err)
	}
	return nil
}

func (e *Election) newLease(now time.Time) Lease {
	return Lease{NodeID: e.nodeID, Acquired: now, ExpiresAt: now.Add(e.ttl)}
}
