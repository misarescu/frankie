-- name: CreateIssue :one
INSERT INTO issues (id, project_id, type, title, spec, acceptance_criteria, status, priority, source_ref, labels, created_by)
VALUES (@id, @project_id, @type, @title, @spec, @acceptance_criteria, @status, @priority, sqlc.narg('source_ref'), @labels, @created_by)
RETURNING *;

-- name: GetIssue :one
SELECT * FROM issues WHERE id = @id;

-- name: GetIssueBySourceRef :one
SELECT * FROM issues WHERE project_id = @project_id AND source_ref = @source_ref;

-- name: ListIssuesByStatus :many
SELECT * FROM issues
WHERE project_id = @project_id AND status = @status
ORDER BY priority DESC, created_at, id;

-- Compare-and-set: only moves the issue if it is still in from_status.
-- name: TransitionIssue :one
UPDATE issues
SET status = @to_status, updated_at = now()
WHERE id = @id AND status = @from_status
RETURNING *;
