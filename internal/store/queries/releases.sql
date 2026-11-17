-- Releases and their deployments (D110). Stages are project configuration;
-- a deployment names its stage.

-- Inserts nothing when the project already has a release of the commit.
-- name: InsertRelease :one
INSERT INTO releases (id, project_id, repo, sha)
VALUES (@id, @project_id, @repo, @sha)
ON CONFLICT (project_id, repo, sha) DO NOTHING
RETURNING *;

-- name: GetRelease :one
SELECT * FROM releases WHERE id = @id;

-- name: GetReleaseByCommit :one
SELECT * FROM releases WHERE project_id = @project_id AND repo = @repo AND sha = @sha;

-- Newest first.
-- name: ListReleasesForProject :many
SELECT * FROM releases WHERE project_id = @project_id
ORDER BY created_at DESC, id DESC
LIMIT @max_rows;

-- Links the issues a release contains: the project's merged issues whose change
-- request in the repo merged no later than the one that made the commit, in the
-- order they merged. A commit no recorded merge made (e.g. one a person pushed)
-- contains every merged issue. Change requests merged before merges were timed
-- count from their last update, which was their merge.
-- name: LinkReleaseIssues :exec
WITH upto AS (
  SELECT coalesce(
    (SELECT max(coalesce(c.merged_at, c.updated_at)) FROM change_requests c
     WHERE c.repo = @repo AND c.status = 'merged' AND c.merge_sha = @sha::text),
    now()) AS t
), merged AS (
  SELECT i.id, min(coalesce(cr.merged_at, cr.updated_at)) AS merged_at
  FROM issues i
  JOIN change_requests cr ON cr.issue_id = i.id AND cr.repo = @repo AND cr.status = 'merged'
  WHERE i.project_id = @project_id AND i.status = 'merged'
  GROUP BY i.id
)
INSERT INTO release_issues (release_id, issue_id, position)
SELECT @release_id, m.id, row_number() OVER (ORDER BY m.merged_at, m.id)
FROM merged m, upto
WHERE m.merged_at <= upto.t;

-- name: ListReleaseIssueIDs :many
SELECT issue_id FROM release_issues WHERE release_id = @release_id ORDER BY position;

-- A reason, when given, replaces the earlier one.
-- name: TransitionRelease :one
UPDATE releases
SET status      = @to_status::text,
    reason      = CASE WHEN @reason::text <> '' THEN @reason::text ELSE reason END,
    finished_at = CASE WHEN @to_status::text IN ('succeeded', 'failed', 'rolled_back') THEN coalesce(finished_at, now()) ELSE finished_at END,
    updated_at  = now()
WHERE id = @id AND status = @from_status::text
RETURNING *;

-- Inserts nothing when the release already has a deployment to the stage.
-- name: InsertDeployment :one
INSERT INTO deployments (id, release_id, stage, driver)
VALUES (@id, @release_id, @stage, @driver)
ON CONFLICT (release_id, stage) DO NOTHING
RETURNING *;

-- name: GetDeployment :one
SELECT * FROM deployments WHERE id = @id;

-- name: GetDeploymentByStage :one
SELECT * FROM deployments WHERE release_id = @release_id AND stage = @stage;

-- In the order they started.
-- name: ListDeployments :many
SELECT * FROM deployments WHERE release_id = @release_id ORDER BY created_at, id;

-- A reason, when given, replaces the earlier one.
-- name: TransitionDeployment :one
UPDATE deployments
SET status      = @to_status::text,
    reason      = CASE WHEN @reason::text <> '' THEN @reason::text ELSE reason END,
    started_at  = CASE WHEN @to_status::text = 'in_progress' THEN coalesce(started_at, now()) ELSE started_at END,
    finished_at = CASE WHEN @to_status::text IN ('succeeded', 'failed', 'rolled_back') THEN coalesce(finished_at, now()) ELSE finished_at END,
    updated_at  = now()
WHERE id = @id AND status = @from_status::text
RETURNING *;

-- name: SetDeploymentDriverState :one
UPDATE deployments SET driver_state = @driver_state, updated_at = now()
WHERE id = @id
RETURNING *;

-- Records the bug issue a failed verification opened, once: the first one stays.
-- name: SetReleaseBugIssue :one
UPDATE releases SET bug_issue_ref = coalesce(bug_issue_ref, @bug_issue_ref::text), updated_at = now()
WHERE id = @id
RETURNING *;
