CREATE TYPE job_status AS ENUM ('pending', 'leased', 'completed', 'failed', 'cancelled');

CREATE TABLE jobs (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    queue TEXT NOT NULL,
    status job_status NOT NULL DEFAULT 'pending',
    idempotency_key TEXT,
    envelope_key TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}',
    priority INT NOT NULL DEFAULT 0,
    attempts INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    leased_at TIMESTAMPTZ,
    leased_by TEXT,
    scheduled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_jobs_status_queue ON jobs (status, queue);
CREATE INDEX idx_jobs_leased_at ON jobs (leased_at) WHERE status = 'leased';
CREATE INDEX idx_jobs_scheduled ON jobs (scheduled_at) WHERE status = 'pending';
CREATE UNIQUE INDEX idx_jobs_idempotency ON jobs (idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE job_domains (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    job_id TEXT NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    domain_kind TEXT NOT NULL,
    domain_id TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_job_domains_job_id ON job_domains (job_id);
CREATE INDEX idx_job_domains_kind_id ON job_domains (domain_kind, domain_id);
