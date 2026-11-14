-- name: CreateIssue :one
INSERT INTO issues (id, project_id, number, key, parent_id, type, title, spec, acceptance_criteria, status, priority, source_ref, labels, created_by)
VALUES (@id, @project_id, @number, @key, sqlc.narg('parent_id'), @type, @title, @spec, @acceptance_criteria, @status, @priority, sqlc.narg('source_ref'), @labels, @created_by)
RETURNING *;

-- name: GetIssue :one
SELECT * FROM issues WHERE id = @id;

-- name: GetIssueByKey :one
SELECT * FROM issues WHERE key = @key;

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

-- name: SetIssueParent :one
UPDATE issues
SET parent_id = sqlc.narg('parent_id'), updated_at = now()
WHERE id = @id
RETURNING *;

-- name: ListIssueChildren :many
SELECT * FROM issues WHERE parent_id = @parent_id
ORDER BY priority DESC, number;

-- The hierarchy of a project's issues, for cycle checks (D57).
-- name: ListIssueNodes :many
SELECT id, key, parent_id FROM issues WHERE project_id = @project_id;

-- name: ListProjectDependencies :many
SELECT d.issue_id, d.depends_on_id
FROM issue_dependencies d JOIN issues i ON i.id = d.issue_id
WHERE i.project_id = @project_id;

-- name: AddIssueDependency :exec
INSERT INTO issue_dependencies (issue_id, depends_on_id)
VALUES (@issue_id, @depends_on_id)
ON CONFLICT DO NOTHING;

-- name: RemoveIssueDependency :exec
DELETE FROM issue_dependencies WHERE issue_id = @issue_id AND depends_on_id = @depends_on_id;

-- The issues this one waits for.
-- name: ListIssueDependencies :many
SELECT * FROM issues
WHERE id IN (SELECT depends_on_id FROM issue_dependencies WHERE issue_id = @issue_id)
ORDER BY number;

-- The issues that wait for this one.
-- name: ListIssueDependents :many
SELECT * FROM issues
WHERE id IN (SELECT issue_id FROM issue_dependencies WHERE depends_on_id = @depends_on_id)
ORDER BY number;

-- A project's issues for the board: model spend across all runs and the latest run.
-- name: ListProjectIssues :many
SELECT sqlc.embed(i),
       (SELECT coalesce(sum(s.cost_usd), 0)::float8
          FROM step_runs s JOIN runs r ON r.id = s.run_id WHERE r.issue_id = i.id) AS cost_usd,
       -- An issue without runs gets the nil UUID and an empty status.
       coalesce(lr.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS latest_run_id,
       coalesce(lr.status, '')::text AS latest_run_status
FROM issues i
LEFT JOIN LATERAL (
  SELECT r.id, r.status FROM runs r WHERE r.issue_id = i.id ORDER BY r.created_at DESC, r.id DESC LIMIT 1
) lr ON true
WHERE i.project_id = @project_id
ORDER BY i.priority DESC, i.number;
