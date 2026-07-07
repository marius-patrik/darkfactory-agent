-- name: EnqueueJob :one
INSERT INTO jobs (queue, idempotency_key, envelope_key, payload, priority, max_attempts, scheduled_at)
VALUES ($1, $2, $3, $4, $5, $6, COALESCE($7, now()))
ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
RETURNING *;

-- name: LeaseJob :one
WITH next_job AS (
    SELECT jobs.id FROM jobs
    WHERE jobs.queue = $1 AND jobs.status = 'pending' AND jobs.scheduled_at <= now()
    ORDER BY jobs.priority DESC, jobs.created_at ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE jobs
SET status = 'leased', leased_at = now(), leased_by = $2, attempts = attempts + 1, updated_at = now()
WHERE jobs.id = (SELECT next_job.id FROM next_job)
RETURNING *;

-- name: CompleteJob :one
UPDATE jobs SET status = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: GetJobByID :one
SELECT * FROM jobs WHERE id = $1 LIMIT 1;

-- name: ListPendingJobsByQueue :many
SELECT * FROM jobs WHERE queue = $1 AND status = 'pending' ORDER BY priority DESC, created_at ASC;
