CREATE TYPE source_class AS ENUM ('verified', 'inferred');

CREATE TABLE memory_embeddings (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    agent_id TEXT NOT NULL,
    session_id TEXT,
    source_class source_class NOT NULL DEFAULT 'inferred',
    hypothesis BOOLEAN NOT NULL DEFAULT false,
    content TEXT NOT NULL,
    embedding vector(1024),
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON COLUMN memory_embeddings.embedding IS 'vector(1024) — dimension is provisional';

CREATE INDEX idx_memory_agent ON memory_embeddings (agent_id);
CREATE INDEX idx_memory_session ON memory_embeddings (session_id);
CREATE INDEX idx_memory_source ON memory_embeddings (source_class, hypothesis);
CREATE INDEX idx_memory_embedding ON memory_embeddings USING ivfflat (embedding vector_cosine_ops);
