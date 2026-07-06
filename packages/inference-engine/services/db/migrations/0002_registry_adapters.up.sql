CREATE TYPE capability_kind AS ENUM ('skill', 'plugin', 'extension', 'hook', 'script', 'model', 'adapter');
CREATE TYPE promotion_state AS ENUM ('candidate', 'active', 'quarantined', 'retired', 'rolled_back');

CREATE TABLE capabilities (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    kind capability_kind NOT NULL,
    name TEXT NOT NULL,
    version TEXT NOT NULL DEFAULT '0.0.0',
    io JSONB NOT NULL DEFAULT '{}',
    permissions JSONB NOT NULL DEFAULT '{}',
    host_reqs JSONB NOT NULL DEFAULT '{}',
    scorecard JSONB NOT NULL DEFAULT '{}',
    lineage JSONB NOT NULL DEFAULT '{}',
    latency JSONB NOT NULL DEFAULT '{}',
    cost JSONB NOT NULL DEFAULT '{}',
    safety JSONB NOT NULL DEFAULT '{}',
    promotion_state promotion_state NOT NULL DEFAULT 'candidate',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_capabilities_name_kind_version ON capabilities (name, kind, version);
CREATE INDEX idx_capabilities_kind ON capabilities (kind);
CREATE INDEX idx_capabilities_promotion_state ON capabilities (promotion_state);

CREATE TABLE adapters (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    capability_id TEXT REFERENCES capabilities (id) ON DELETE SET NULL,
    base_model TEXT NOT NULL,
    role_binding TEXT,
    skill_binding TEXT,
    adapter_path TEXT,
    eval_refs JSONB NOT NULL DEFAULT '[]',
    promotion_state promotion_state NOT NULL DEFAULT 'candidate',
    lineage JSONB NOT NULL DEFAULT '{}',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_adapters_capability_id ON adapters (capability_id);
CREATE INDEX idx_adapters_base_model ON adapters (base_model);
CREATE INDEX idx_adapters_promotion_state ON adapters (promotion_state);

CREATE TABLE model_routes (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    model_id TEXT NOT NULL UNIQUE,
    engine TEXT NOT NULL,
    desired_replicas INT NOT NULL DEFAULT 1,
    current_replicas INT NOT NULL DEFAULT 0,
    active BOOLEAN NOT NULL DEFAULT true,
    config JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE model_health (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    model_route_id TEXT NOT NULL REFERENCES model_routes (id) ON DELETE CASCADE,
    node TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'unknown',
    latency_ms INT,
    last_check TIMESTAMPTZ,
    error_count INT NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (model_route_id, node)
);

CREATE INDEX idx_model_health_route ON model_health (model_route_id);
CREATE INDEX idx_model_health_status ON model_health (status);
