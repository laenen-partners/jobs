-- name: StartStep :one
INSERT INTO job_steps (job_id, name, sequence, status, input, started_at)
VALUES (@job_id, @name, @sequence, 'running', @input, now())
RETURNING *;

-- name: CompleteStep :exec
UPDATE job_steps
SET status = 'completed', output = @output, completed_at = now(), updated_at = now()
WHERE id = @id;

-- name: FailStep :exec
UPDATE job_steps
SET status = 'failed', error = @error, completed_at = now(), updated_at = now()
WHERE id = @id;

-- name: GetStepsByJobID :many
SELECT * FROM job_steps
WHERE job_id = @job_id
ORDER BY sequence ASC, created_at ASC;
