CREATE TYPE config_layer AS ENUM ('global', 'project', 'agent', 'node', 'session');

CREATE TABLE config_projection (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    layer config_layer NOT NULL,
    project_id TEXT NOT NULL DEFAULT '',
    agent_id TEXT NOT NULL DEFAULT '',
    node_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    key TEXT NOT NULL,
    value JSONB NOT NULL DEFAULT '{}',
    precedence INT NOT NULL DEFAULT 0,
    source TEXT NOT NULL DEFAULT 'unknown',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (layer, project_id, agent_id, node_id, session_id, key)
);

CREATE INDEX idx_config_projection_lookup ON config_projection (layer, key);

COMMENT ON TABLE config_projection IS 'Effective config projected from config.yaml layers';

CREATE TABLE sync_manifests (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    hash TEXT NOT NULL UNIQUE,
    class TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    refs JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sync_manifests_class ON sync_manifests (class);

CREATE TABLE consolidation_runs (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    session_id TEXT NOT NULL,
    run_id TEXT,
    checkpoint_kind TEXT NOT NULL,
    reflection JSONB NOT NULL DEFAULT '{}',
    artifacts JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_consolidation_session ON consolidation_runs (session_id);
CREATE INDEX idx_consolidation_run ON consolidation_runs (run_id);

CREATE TYPE eval_status AS ENUM ('running', 'passed', 'failed', 'aborted');

CREATE TABLE eval_runs (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    candidate_id TEXT NOT NULL,
    candidate_kind TEXT NOT NULL,
    baseline_id TEXT,
    scorecard JSONB NOT NULL DEFAULT '{}',
    status eval_status NOT NULL DEFAULT 'running',
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_eval_runs_candidate ON eval_runs (candidate_id, candidate_kind);
CREATE INDEX idx_eval_runs_status ON eval_runs (status);

CREATE TABLE eval_results (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    eval_run_id TEXT NOT NULL REFERENCES eval_runs (id) ON DELETE CASCADE,
    test_case_id TEXT NOT NULL,
    metric TEXT NOT NULL,
    value NUMERIC NOT NULL,
    delta NUMERIC,
    outcome TEXT NOT NULL DEFAULT 'pending',
    reviewer TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_eval_results_run ON eval_results (eval_run_id);

CREATE TYPE canary_stage AS ENUM ('shadow', 'pct_1', 'pct_10', 'pct_100');

CREATE TABLE canary_state (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    eval_run_id TEXT NOT NULL REFERENCES eval_runs (id) ON DELETE CASCADE,
    capability_id TEXT REFERENCES capabilities (id) ON DELETE SET NULL,
    stage canary_stage NOT NULL DEFAULT 'shadow',
    traffic_percent INT NOT NULL DEFAULT 0,
    gates JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'running',
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_canary_eval ON canary_state (eval_run_id);
CREATE INDEX idx_canary_capability ON canary_state (capability_id);
