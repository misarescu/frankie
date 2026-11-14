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

-- Adds a session's usage: a step's retried session attempts each add theirs.
-- name: AddStepRunUsage :one
UPDATE step_runs
SET cost_usd      = cost_usd + @cost_usd::float8,
    input_tokens  = input_tokens + @input_tokens::bigint,
    output_tokens = output_tokens + @output_tokens::bigint,
    updated_at    = now()
WHERE id = @id
RETURNING *;

-- What all runs of an issue have spent on models so far (D53).
-- name: IssueSpend :one
SELECT coalesce(sum(s.cost_usd), 0)::float8 AS spent
FROM step_runs s JOIN runs r ON r.id = s.run_id
WHERE r.issue_id = @issue_id;

-- name: ListRecentRuns :many
SELECT r.id, r.status, r.created_at, r.started_at, r.finished_at, i.key AS issue_key, i.title AS issue_title,
       (SELECT coalesce(sum(s.cost_usd), 0)::float8 FROM step_runs s WHERE s.run_id = r.id) AS cost_usd
FROM runs r JOIN issues i ON i.id = r.issue_id
ORDER BY r.created_at DESC
LIMIT @max_runs;

-- Records how many times a gate has looped back (docs/spec/03: loop counts are recorded per issue).
-- name: SetRunLoopCount :one
UPDATE runs
SET loop_counts = jsonb_set(loop_counts, ARRAY[@step_id::text], to_jsonb(@loops::integer)),
    updated_at  = now()
WHERE id = @id
RETURNING *;
