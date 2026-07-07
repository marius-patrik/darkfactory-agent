-- name: AcquireClaim :one
INSERT INTO claims (scope, resource_path, run_id, node, owner, ttl, expires_at, provenance)
VALUES ($1, $2, $3, $4, $5, $6::interval, now() + $6::interval, $7)
ON CONFLICT (scope, resource_path) WHERE state = 'active' DO NOTHING
RETURNING *;

-- name: HeartbeatClaim :one
UPDATE claims
SET expires_at = now() + ttl::interval, updated_at = now()
WHERE id = $1 AND state = 'active' AND suspended_at IS NULL
RETURNING *;

-- name: ReleaseClaim :one
UPDATE claims
SET state = 'released', expires_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SuspendAllClaims :many
UPDATE claims
SET state = 'suspended', suspended_at = now(), updated_at = now()
WHERE state = 'active'
RETURNING *;

-- name: ResumeAllClaims :many
UPDATE claims
SET state = 'active',
    resumed_at = now(),
    expires_at = now() + (expires_at - suspended_at),
    suspended_at = NULL,
    updated_at = now()
WHERE state = 'suspended'
RETURNING *;

-- name: GetClaimByID :one
SELECT * FROM claims WHERE id = $1 LIMIT 1;

-- name: ListActiveClaimsByRun :many
SELECT * FROM claims WHERE run_id = $1 AND state = 'active';
