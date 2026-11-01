CREATE TABLE IF NOT EXISTS jobs (
    id             TEXT PRIMARY KEY,
    tenant_id      TEXT        NOT NULL,
    file_name      TEXT        NOT NULL,
    content_type   TEXT        NOT NULL,
    size_bytes     BIGINT      NOT NULL,
    staging_object TEXT        NOT NULL,
    status         TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS jobs_tenant_created_idx ON jobs (tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS outbox (
    id           TEXT PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    aggregate_id TEXT        NOT NULL,
    tenant_id    TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS outbox_unpublished_idx ON outbox (created_at) WHERE published_at IS NULL;
