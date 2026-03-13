-- name: CreateJob :one
INSERT INTO jobs (external_reference, job_type, status, input, metadata, tags)
VALUES (@external_reference, @job_type, @status, @input, @metadata, @tags)
RETURNING *;

-- name: GetJob :one
SELECT * FROM jobs WHERE id = @id;

-- name: GetJobByExternalReference :one
SELECT * FROM jobs WHERE external_reference = @external_reference;

-- name: FinalizeJob :execrows
UPDATE jobs
SET status = @status, error = @error, output = @output, tags = @tags, updated_at = now()
WHERE id = @id AND status NOT IN ('completed', 'failed', 'cancelled');

-- name: ReportProgress :exec
UPDATE jobs
SET status = 'running',
    progress_step = @progress_step,
    progress_current = @progress_current,
    progress_total = @progress_total,
    progress_message = @progress_message,
    progress_updated_at = now(),
    tags = @tags,
    updated_at = now()
WHERE id = @id;

-- name: ListJobs :many
SELECT * FROM jobs
WHERE (sqlc.narg('tags')::text[] IS NULL OR tags @> sqlc.narg('tags')::text[])
ORDER BY created_at DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: AddJobTags :exec
UPDATE jobs
SET tags = (SELECT ARRAY(SELECT DISTINCT unnest(tags || @new_tags::text[]))),
    updated_at = now()
WHERE id = @id;

-- name: GetJobTags :one
SELECT tags FROM jobs WHERE id = @id;
