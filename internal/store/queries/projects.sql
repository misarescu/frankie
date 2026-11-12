-- name: CreateProject :one
INSERT INTO projects (id, name)
VALUES (@id, @name)
RETURNING *;

-- name: GetProjectByName :one
SELECT * FROM projects WHERE name = @name;
