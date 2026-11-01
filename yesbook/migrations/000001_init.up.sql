-- DRAFT: AI-generated schema. Review indexes, constraints and retention
-- before applying to a production Cloud SQL instance.

CREATE TABLE jobs (
    id          text        PRIMARY KEY,
    tenant_id   text        NOT NULL,
    file_name   text        NOT NULL,
    size_bytes  bigint      NOT NULL CHECK (size_bytes > 0),
    status      text        NOT NULL,
    file_hash   text        NOT NULL DEFAULT '',
    version_id  text        NOT NULL DEFAULT '',
    reason      text        NOT NULL DEFAULT '',
    claimed_by  text        NOT NULL DEFAULT '',
    revision    integer     NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL
);

CREATE INDEX jobs_tenant_created_idx ON jobs (tenant_id, created_at DESC);
-- Supports the stuck-job alert query.
CREATE INDEX jobs_active_idx ON jobs (status, updated_at)
    WHERE status IN ('queued', 'processing');

CREATE TABLE versions (
    id               text             PRIMARY KEY,
    tenant_id        text             NOT NULL,
    job_id           text             NOT NULL REFERENCES jobs (id),
    file_hash        text             NOT NULL,
    pipeline_version text             NOT NULL,
    status           text             NOT NULL,
    coverage         double precision NOT NULL,
    content_object   text             NOT NULL,
    original_object  text             NOT NULL,
    created_at       timestamptz      NOT NULL
);

CREATE INDEX versions_tenant_hash_idx ON versions (tenant_id, file_hash);

-- Only versions with status ready are cached, so a hash match never
-- returns partial output.
CREATE TABLE cache_keys (
    key        text        PRIMARY KEY,
    version_id text        NOT NULL REFERENCES versions (id),
    created_at timestamptz NOT NULL
);

CREATE TABLE attempts (
    id                text        PRIMARY KEY,
    job_id            text        NOT NULL REFERENCES jobs (id),
    target_id         text        NOT NULL,
    extractor         text        NOT NULL,
    extractor_version text        NOT NULL,
    succeeded         boolean     NOT NULL,
    error             text        NOT NULL DEFAULT '',
    cost_micros       bigint      NOT NULL DEFAULT 0,
    started_at        timestamptz NOT NULL,
    finished_at       timestamptz NOT NULL
);

CREATE INDEX attempts_job_idx ON attempts (job_id);

CREATE TABLE quality_results (
    version_id        text  NOT NULL REFERENCES versions (id),
    target_id         text  NOT NULL,
    extractor         text  NOT NULL,
    extractor_version text  NOT NULL,
    verdict           text  NOT NULL,
    reason            text  NOT NULL DEFAULT '',
    checks            jsonb NOT NULL,
    PRIMARY KEY (version_id, target_id)
);

-- Events carry IDs and status only; consumers fetch content from the
-- Content API.
CREATE TABLE outbox (
    seq          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id           text        NOT NULL UNIQUE,
    event_type   text        NOT NULL,
    job_id       text        NOT NULL,
    tenant_id    text        NOT NULL,
    payload      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL,
    published_at timestamptz
);

CREATE INDEX outbox_pending_idx ON outbox (seq) WHERE published_at IS NULL;
