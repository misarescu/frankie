-- name: CreateRun :one
INSERT INTO runs (id, issue_id, workflow_snapshot, workflow_version, temporal_workflow_id, temporal_run_id)
VALUES (@id, @issue_id, @workflow_snapshot, @workflow_version, @temporal_workflow_id, sqlc.narg('temporal_run_id'))
RETURNING *;

-- name: GetRunByTemporalRunID :one
SELECT * FROM runs WHERE temporal_run_id = @temporal_run_id;

-- name: GetRun :one
SELECT * FROM runs WHERE id = @id;

-- name: GetActiveRunForIssue :one
SELECT * FROM runs WHERE issue_id = @issue_id AND status IN ('pending', 'running');

-- Compare-and-set; stamps started_at on first entry to running and finished_at on a terminal status.
-- name: TransitionRun :one
UPDATE runs
SET status      = @to_status::text,
    started_at  = CASE WHEN @to_status::text = 'running' THEN coalesce(started_at, now()) ELSE started_at END,
    finished_at = CASE WHEN @to_status::text IN ('succeeded', 'failed', 'canceled') THEN now() ELSE finished_at END,
    updated_at  = now()
WHERE id = @id AND status = @from_status::text
RETURNING *;

-- name: CreateStepRun :one
INSERT INTO step_runs (id, run_id, step_id, attempt, kind)
VALUES (@id, @run_id, @step_id, @attempt, @kind)
RETURNING *;

-- name: GetStepRun :one
SELECT * FROM step_runs WHERE id = @id;

-- name: ListStepRuns :many
SELECT * FROM step_runs WHERE run_id = @run_id ORDER BY created_at, id;

-- name: TransitionStepRun :one
UPDATE step_runs
SET status      = @to_status::text,
    started_at  = CASE WHEN @to_status::text = 'running' THEN coalesce(started_at, now()) ELSE started_at END,
    finished_at = CASE WHEN @to_status::text IN ('succeeded', 'failed', 'canceled') THEN now() ELSE finished_at END,
    updated_at  = now()
WHERE id = @id AND status = @from_status::text
RETURNING *;

-- name: SetStepRunPod :one
UPDATE step_runs SET pod_name = @pod_name, updated_at = now() WHERE id = @id RETURNING *;

-- name: SetStepRunCommit :one
UPDATE step_runs SET commit_sha = @commit_sha, updated_at = now() WHERE id = @id RETURNING *;
