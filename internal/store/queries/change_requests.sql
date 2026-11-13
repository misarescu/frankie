-- Idempotent: re-recording the same provider change request updates its head.
-- name: UpsertChangeRequest :one
INSERT INTO change_requests (id, issue_id, run_id, repo, number, provider_ref, branch, head_sha)
VALUES (@id, @issue_id, @run_id, @repo, @number, @provider_ref, @branch, @head_sha)
ON CONFLICT (repo, number) DO UPDATE
SET head_sha = EXCLUDED.head_sha, run_id = EXCLUDED.run_id, updated_at = now()
RETURNING *;

-- name: ListChangeRequestsForIssue :many
SELECT * FROM change_requests WHERE issue_id = @issue_id ORDER BY created_at;
