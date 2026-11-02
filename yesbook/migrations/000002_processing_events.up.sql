CREATE TABLE processing_events (
    seq              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    id               text        NOT NULL UNIQUE,
    tenant_id        text        NOT NULL,
    job_id            text        NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    workflow_id       text        NOT NULL,
    run_id            text        NOT NULL,
    retry             integer     NOT NULL CHECK (retry >= 0),
    stage             text        NOT NULL CHECK (stage IN (
        'verify', 'cache_lookup', 'prepare', 'extract', 'validate', 'assemble', 'publish'
    )),
    status            text        NOT NULL CHECK (status IN (
        'started', 'progress', 'completed', 'failed', 'skipped'
    )),
    progress_current  integer,
    progress_total    integer,
    progress_unit     text,
    reason            text        NOT NULL DEFAULT '',
    occurred_at       timestamptz NOT NULL,
    CHECK (
        (progress_current IS NULL AND progress_total IS NULL AND progress_unit IS NULL)
        OR (
            progress_current IS NOT NULL
            AND progress_total IS NOT NULL
            AND progress_unit IS NOT NULL
            AND
            progress_current >= 0
            AND progress_total > 0
            AND progress_current <= progress_total
            AND char_length(progress_unit) BETWEEN 1 AND 32
        )
    ),
    CHECK (status <> 'progress' OR progress_current IS NOT NULL),
    CHECK (char_length(reason) <= 2048)
);

CREATE INDEX processing_events_tenant_job_seq_idx
    ON processing_events (tenant_id, job_id, seq);
