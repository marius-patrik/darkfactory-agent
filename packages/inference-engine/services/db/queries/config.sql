-- name: GetEffectiveConfig :many
SELECT DISTINCT ON (key)
    layer, key, value, precedence, source
FROM config_projection
WHERE layer = 'global'
   OR (layer = 'project' AND project_id = $1)
   OR (layer = 'agent'   AND agent_id   = $2)
   OR (layer = 'node'    AND node_id    = $3)
   OR (layer = 'session' AND session_id = $4)
ORDER BY key, precedence DESC, updated_at DESC;

-- name: UpsertConfig :one
INSERT INTO config_projection (
    layer, project_id, agent_id, node_id, session_id, key, value, precedence, source
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (layer, project_id, agent_id, node_id, session_id, key) DO UPDATE SET
    value = EXCLUDED.value,
    precedence = EXCLUDED.precedence,
    source = EXCLUDED.source,
    updated_at = now()
RETURNING *;
