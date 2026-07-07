-- name: UpsertCapability :one
INSERT INTO capabilities (
    id, kind, name, version, io, permissions, host_reqs, scorecard, lineage,
    latency, cost, safety, promotion_state, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (id) DO UPDATE SET
    kind = EXCLUDED.kind,
    name = EXCLUDED.name,
    version = EXCLUDED.version,
    io = EXCLUDED.io,
    permissions = EXCLUDED.permissions,
    host_reqs = EXCLUDED.host_reqs,
    scorecard = EXCLUDED.scorecard,
    lineage = EXCLUDED.lineage,
    latency = EXCLUDED.latency,
    cost = EXCLUDED.cost,
    safety = EXCLUDED.safety,
    promotion_state = EXCLUDED.promotion_state,
    metadata = EXCLUDED.metadata,
    updated_at = now()
RETURNING *;

-- name: GetCapability :one
SELECT * FROM capabilities WHERE id = $1 LIMIT 1;

-- name: ListCapabilitiesByKind :many
SELECT * FROM capabilities WHERE kind = $1 ORDER BY name, version;

-- name: ListCapabilitiesByPromotionState :many
SELECT * FROM capabilities WHERE promotion_state = $1 ORDER BY name;
