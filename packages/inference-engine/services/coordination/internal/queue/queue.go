// Package queue provides a Postgres-backed work queue.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrEmpty = errors.New("queue empty")

type Job struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Payload     json.RawMessage `json:"payload"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	ClaimedBy   string          `json:"claimed_by,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	AvailableAt time.Time       `json:"available_at"`
}

type Counts struct {
	Ready    int `json:"ready"`
	Claimed  int `json:"claimed"`
	Done     int `json:"done"`
	Failed   int `json:"failed"`
	Deferred int `json:"deferred"`
}

type Queue interface {
	Enqueue(ctx context.Context, job Job) (string, error)
	Dequeue(ctx context.Context, workerID string, visibilityTimeout time.Duration) (Job, error)
	Ack(ctx context.Context, id string) error
	Nack(ctx context.Context, id string, delay time.Duration) error
	Counts(ctx context.Context) (Counts, error)
}

type PGQueue struct {
	pool *pgxpool.Pool
}

func NewPG(ctx context.Context, dsn string) (*PGQueue, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	q := &PGQueue{pool: pool}
	if err := q.Init(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return q, nil
}

func (q *PGQueue) Close() { q.pool.Close() }

// Init creates the coordination-owned queue table using a coordination_ prefix.
func (q *PGQueue) Init(ctx context.Context) error {
	_, err := q.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS coordination_queue (
	id text PRIMARY KEY,
	kind text NOT NULL,
	payload jsonb NOT NULL DEFAULT '{}'::jsonb,
	status text NOT NULL DEFAULT 'ready',
	attempts integer NOT NULL DEFAULT 0,
	max_attempts integer NOT NULL DEFAULT 3,
	claimed_by text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL DEFAULT now(),
	available_at timestamptz NOT NULL DEFAULT now(),
	claimed_at timestamptz,
	done_at timestamptz,
	last_error text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS coordination_queue_ready_idx
ON coordination_queue (status, available_at, created_at);`)
	return err
}

func (q *PGQueue) Enqueue(ctx context.Context, job Job) (string, error) {
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	if job.Kind == "" {
		return "", errors.New("kind is required")
	}
	if len(job.Payload) == 0 {
		job.Payload = json.RawMessage(`{}`)
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = 3
	}
	if job.AvailableAt.IsZero() {
		job.AvailableAt = time.Now().UTC()
	}
	_, err := q.pool.Exec(ctx, `
INSERT INTO coordination_queue (id, kind, payload, max_attempts, available_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO NOTHING`, job.ID, job.Kind, job.Payload, job.MaxAttempts, job.AvailableAt)
	return job.ID, err
}

func (q *PGQueue) Dequeue(ctx context.Context, workerID string, visibilityTimeout time.Duration) (Job, error) {
	if workerID == "" {
		return Job{}, errors.New("worker id is required")
	}
	if visibilityTimeout <= 0 {
		visibilityTimeout = time.Minute
	}
	var job Job
	err := q.pool.QueryRow(ctx, `
WITH candidate AS (
	SELECT id FROM coordination_queue
	WHERE status = 'ready' AND available_at <= now() AND attempts < max_attempts
	ORDER BY created_at
	FOR UPDATE SKIP LOCKED
	LIMIT 1
)
UPDATE coordination_queue q
SET status='claimed', claimed_by=$1, claimed_at=now(),
	attempts=q.attempts+1, available_at=now()+$2::interval
FROM candidate
WHERE q.id = candidate.id
RETURNING q.id, q.kind, q.payload, q.attempts, q.max_attempts, q.claimed_by, q.created_at, q.available_at`,
		workerID, fmt.Sprintf("%f seconds", visibilityTimeout.Seconds()),
	).Scan(&job.ID, &job.Kind, &job.Payload, &job.Attempts, &job.MaxAttempts, &job.ClaimedBy, &job.CreatedAt, &job.AvailableAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrEmpty
	}
	return job, err
}

func (q *PGQueue) Ack(ctx context.Context, id string) error {
	tag, err := q.pool.Exec(ctx, `UPDATE coordination_queue SET status='done', done_at=now() WHERE id=$1 AND status='claimed'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("claimed job %s not found", id)
	}
	return nil
}

func (q *PGQueue) Nack(ctx context.Context, id string, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	tag, err := q.pool.Exec(ctx, `
UPDATE coordination_queue
SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'ready' END,
	claimed_by='', available_at=now()+$2::interval
WHERE id=$1 AND status='claimed'`, id, fmt.Sprintf("%f seconds", delay.Seconds()))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("claimed job %s not found", id)
	}
	return nil
}

func (q *PGQueue) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := q.pool.QueryRow(ctx, `
SELECT
	COUNT(*) FILTER (WHERE status='ready' AND available_at <= now()),
	COUNT(*) FILTER (WHERE status='claimed'),
	COUNT(*) FILTER (WHERE status='done'),
	COUNT(*) FILTER (WHERE status='failed'),
	COUNT(*) FILTER (WHERE status='ready' AND available_at > now())
FROM coordination_queue`).Scan(&c.Ready, &c.Claimed, &c.Done, &c.Failed, &c.Deferred)
	return c, err
}
