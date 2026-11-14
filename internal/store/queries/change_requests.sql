-- Idempotent: re-recording the same provider change request updates its head.
-- name: UpsertChangeRequest :one
INSERT INTO change_requests (id, issue_id, run_id, repo, number, provider_ref, branch, head_sha)
VALUES (@id, @issue_id, @run_id, @repo, @number, @provider_ref, @branch, @head_sha)
ON CONFLICT (repo, number) DO UPDATE
SET head_sha = EXCLUDED.head_sha, run_id = EXCLUDED.run_id, updated_at = now()
RETURNING *;

-- name: ListChangeRequestsForIssue :many
SELECT * FROM change_requests WHERE issue_id = @issue_id ORDER BY created_at;

-- name: GetChangeRequest :one
SELECT * FROM change_requests WHERE id = @id;

-- Open change requests whose issue waits for a person: the ones that can be merged
-- on the provider (D61).
-- name: ListChangeRequestsAwaitingMerge :many
SELECT * FROM change_requests
WHERE status = 'open' AND issue_id IN (SELECT id FROM issues WHERE status = 'awaiting_human')
ORDER BY created_at, id;

-- Compare-and-set from open to merged or closed.
-- name: FinishChangeRequest :one
UPDATE change_requests
SET status = @status, updated_at = now()
WHERE id = @id AND status = 'open'
RETURNING *;
