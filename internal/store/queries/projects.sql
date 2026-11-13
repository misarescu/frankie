-- name: CreateProject :one
INSERT INTO projects (id, name, key_prefix)
VALUES (@id, @name, @key_prefix)
RETURNING *;

-- name: GetProjectByName :one
SELECT * FROM projects WHERE name = @name;

-- Allocates the next issue number; the row lock serializes concurrent callers.
-- name: AllocateIssueNumber :one
UPDATE projects
SET next_issue_number = next_issue_number + 1, updated_at = now()
WHERE id = @id
RETURNING key_prefix, (next_issue_number - 1)::integer AS number;
