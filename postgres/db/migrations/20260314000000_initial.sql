-- migrate:up
CREATE TABLE IF NOT EXISTS jobs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    external_reference  TEXT NOT NULL UNIQUE,
    job_type            TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'pending',
    progress_step       TEXT NOT NULL DEFAULT '',
    progress_current    INT NOT NULL DEFAULT 0,
    progress_total      INT NOT NULL DEFAULT 0,
    progress_message    TEXT NOT NULL DEFAULT '',
    progress_updated_at TIMESTAMPTZ,
    error               TEXT NOT NULL DEFAULT '',
    input               JSONB,
    output              JSONB,
    metadata            JSONB,
    tags                TEXT[] NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_jobs_tags_gin ON jobs USING GIN (tags);
CREATE INDEX idx_jobs_status ON jobs (status);

CREATE TABLE IF NOT EXISTS job_steps (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id       UUID NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    sequence     INT NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'running',
    error        TEXT NOT NULL DEFAULT '',
    input        JSONB,
    output       JSONB,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_job_steps_job_id ON job_steps (job_id);

-- migrate:down
DROP TABLE IF EXISTS job_steps;
DROP TABLE IF EXISTS jobs;
