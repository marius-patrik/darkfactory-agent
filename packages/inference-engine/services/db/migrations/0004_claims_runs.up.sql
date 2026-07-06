CREATE TYPE claim_state AS ENUM ('active', 'released', 'expired', 'suspended');

CREATE TABLE claims (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    scope TEXT NOT NULL,
    resource_path TEXT NOT NULL,
    run_id TEXT NOT NULL,
    node TEXT NOT NULL,
    owner TEXT NOT NULL,
    state claim_state NOT NULL DEFAULT 'active',
    ttl INTERVAL NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    suspended_at TIMESTAMPTZ,
    resumed_at TIMESTAMPTZ,
    provenance JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_claims_active_scope_path ON claims (scope, resource_path) WHERE state = 'active';
CREATE INDEX idx_claims_run_id ON claims (run_id);
CREATE INDEX idx_claims_state ON claims (state);
CREATE INDEX idx_claims_expires_at ON claims (expires_at);

CREATE TYPE run_status_value AS ENUM (
    'useful_result',
    'no_artifact',
    'missing_evidence',
    'unresolved',
    'blocked',
    'failed',
    'released',
    'expired'
);

CREATE TABLE run_status (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    run_id TEXT NOT NULL UNIQUE,
    session_id TEXT,
    status run_status_value NOT NULL DEFAULT 'unresolved',
    artifacts JSONB NOT NULL DEFAULT '{}',
    evidence JSONB NOT NULL DEFAULT '{}',
    source_separated JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_run_status_status ON run_status (status);
CREATE INDEX idx_run_status_session_id ON run_status (session_id);
