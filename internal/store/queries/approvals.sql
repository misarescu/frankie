-- Idempotent per gate StepRun: a retried Activity gets the approval it opened.
-- name: OpenApproval :one
INSERT INTO approvals (id, step_run_id, run_id, reason, commit_sha, change_request_url, expires_at)
VALUES (@id, @step_run_id, @run_id, @reason, sqlc.narg('commit_sha'), sqlc.narg('change_request_url'), @expires_at)
ON CONFLICT (step_run_id) DO UPDATE SET updated_at = approvals.updated_at
RETURNING *;

-- name: GetApprovalByStepRun :one
SELECT * FROM approvals WHERE step_run_id = @step_run_id;

-- Compare-and-set from pending, so the first decision wins.
-- name: DecideApproval :one
UPDATE approvals
SET status = @status::text, decided_by = sqlc.narg('decided_by'), comment = sqlc.narg('comment'),
    decided_at = now(), updated_at = now()
WHERE step_run_id = @step_run_id AND status = 'pending'
RETURNING *;

-- name: ExtendApproval :one
UPDATE approvals SET expires_at = @expires_at, updated_at = now()
WHERE step_run_id = @step_run_id AND status = 'pending'
RETURNING *;

-- The approvals inbox: pending decisions with what a human needs to decide
-- and what the API needs to signal the run's workflow.
-- name: ListPendingApprovals :many
SELECT a.*, s.step_id, i.key AS issue_key, i.title AS issue_title,
       r.temporal_workflow_id, r.temporal_run_id
FROM approvals a
JOIN step_runs s ON s.id = a.step_run_id
JOIN runs r ON r.id = a.run_id
JOIN issues i ON i.id = r.issue_id
WHERE a.status = 'pending'
ORDER BY a.created_at, a.id;

-- name: GetPendingApproval :one
SELECT a.*, s.step_id, i.key AS issue_key, i.title AS issue_title,
       r.temporal_workflow_id, r.temporal_run_id
FROM approvals a
JOIN step_runs s ON s.id = a.step_run_id
JOIN runs r ON r.id = a.run_id
JOIN issues i ON i.id = r.issue_id
WHERE a.step_run_id = @step_run_id AND a.status = 'pending';
