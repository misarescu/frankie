-- name: CreateRun :one
INSERT INTO runs (id, issue_id, release_id, workflow_snapshot, workflow_version, temporal_workflow_id, temporal_run_id)
VALUES (@id, sqlc.narg('issue_id'), sqlc.narg('release_id'), @workflow_snapshot, @workflow_version, @temporal_workflow_id, sqlc.narg('temporal_run_id'))
RETURNING *;

-- A release's agent runs (D129), newest first.
-- name: ListReleaseRuns :many
SELECT * FROM runs WHERE release_id = @release_id ORDER BY created_at DESC, id DESC;

-- Holds the release's row until the transaction ends, so that its run is
-- created once.
-- name: LockRelease :one
SELECT id FROM releases WHERE id = @id FOR UPDATE;

-- What a release's agent sessions have spent on models so far (D129).
-- name: ReleaseSpend :one
SELECT coalesce(sum(s.cost_usd), 0)::float8 AS spent
FROM step_runs s JOIN runs r ON r.id = s.run_id
WHERE r.release_id = @release_id;

-- name: GetRunByTemporalRunID :one
SELECT * FROM runs WHERE temporal_run_id = @temporal_run_id;

-- name: GetRun :one
SELECT * FROM runs WHERE id = @id;

-- name: GetActiveRunForIssue :one
SELECT * FROM runs WHERE issue_id = @issue_id::uuid AND status IN ('pending', 'running');

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
WHERE r.issue_id = @issue_id::uuid;

-- Issue runs and release runs (D129); a release run has no issue.
-- name: ListRecentRuns :many
SELECT r.id, r.status, r.created_at, r.started_at, r.finished_at,
       coalesce(i.key, '')::text AS issue_key, coalesce(i.title, '')::text AS issue_title,
       r.release_id, coalesce(rel.sha, '')::text AS release_sha, coalesce(rel.version, '')::text AS release_version,
       (SELECT coalesce(sum(s.cost_usd), 0)::float8 FROM step_runs s WHERE s.run_id = r.id) AS cost_usd
FROM runs r
LEFT JOIN issues i ON i.id = r.issue_id
LEFT JOIN releases rel ON rel.id = r.release_id
ORDER BY r.created_at DESC
LIMIT @max_runs;

-- Records how many times a gate has looped back (docs/spec/03: loop counts are recorded per issue).
-- name: SetRunLoopCount :one
UPDATE runs
SET loop_counts = jsonb_set(loop_counts, ARRAY[@step_id::text], to_jsonb(@loops::integer)),
    updated_at  = now()
WHERE id = @id
RETURNING *;

-- The active runs of an issue and of every issue under it, oldest first: what
-- the issue page's live view follows (M5.1). UNION stops at a hierarchy loop.
-- name: ListActiveRunsUnder :many
WITH RECURSIVE sub (sub_id) AS (
  SELECT top.id FROM issues top WHERE top.id = @issue_id
  UNION
  SELECT c.id FROM issues c JOIN sub ON c.parent_id = sub.sub_id
)
SELECT r.id, r.status, r.workflow_version, r.created_at, i.key AS issue_key, i.title AS issue_title
FROM runs r
JOIN sub ON sub.sub_id = r.issue_id
JOIN issues i ON i.id = r.issue_id
WHERE r.status IN ('pending', 'running')
ORDER BY r.created_at, r.id;

-- An issue's runs, newest first, with what each spent (D53).
-- name: ListIssueRuns :many
SELECT r.id, r.status, r.created_at, r.started_at, r.finished_at, r.workflow_version,
       (SELECT coalesce(sum(s.cost_usd), 0)::float8 FROM step_runs s WHERE s.run_id = r.id) AS cost_usd
FROM runs r
WHERE r.issue_id = @issue_id::uuid
ORDER BY r.created_at DESC, r.id DESC;

-- What all runs of an issue used: cost and tokens (D53).
-- name: IssueUsage :one
SELECT coalesce(sum(s.cost_usd), 0)::float8 AS cost_usd,
       coalesce(sum(s.input_tokens), 0)::bigint AS input_tokens,
       coalesce(sum(s.output_tokens), 0)::bigint AS output_tokens
FROM step_runs s JOIN runs r ON r.id = s.run_id
WHERE r.issue_id = @issue_id::uuid;

-- What a run's steps wait for from a person (M5.1): a pending human gate
-- decision ('approval') or a pending ask-human answer ('answer').
-- name: ListRunWaits :many
SELECT a.step_run_id, 'approval'::text AS waits_for
FROM approvals a WHERE a.run_id = @run_id AND a.status = 'pending'
UNION ALL
SELECT q.step_run_id, 'answer'::text AS waits_for
FROM questions q JOIN step_runs s ON s.id = q.step_run_id
WHERE s.run_id = @run_id AND q.status = 'pending'
UNION ALL
-- A step whose image is being built (D93): its newest image_build event says
-- building, and the step has not ended.
SELECT s.id AS step_run_id, (CASE WHEN b.payload->>'image' = 'dependencies' THEN 'dependency-image' ELSE 'toolchain-image' END)::text AS waits_for
FROM step_runs s
JOIN LATERAL (SELECT e.payload FROM events e
              WHERE e.run_id = @run_id AND e.step_run_id = s.id AND e.type = 'image_build'
              ORDER BY e.seq DESC LIMIT 1) b ON true
WHERE s.run_id = @run_id AND s.status IN ('pending', 'running') AND b.payload->>'status' = 'building';
