-- name: CreateIssue :one
INSERT INTO issues (id, project_id, number, key, parent_id, type, title, spec, acceptance_criteria, status, priority, source_ref, labels, created_by, file_hints)
VALUES (@id, @project_id, @number, @key, sqlc.narg('parent_id'), @type, @title, @spec, @acceptance_criteria, @status, @priority, sqlc.narg('source_ref'), @labels, @created_by, @file_hints)
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
SELECT id, key, type, parent_id FROM issues WHERE project_id = @project_id;

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

-- Sync from an issue source (D60, D64): a draft takes the source's title, body,
-- labels, type and file hints; once its spec is approved (or the scheduler made it
-- ready), the factory's copy is authoritative. A changed text lifts a rejection.
-- name: UpdateDraftIssueFromSource :one
UPDATE issues
SET title = @title, spec = @spec, labels = @labels, type = @type, file_hints = @file_hints,
    spec_rejected_at = NULL, updated_at = now()
WHERE id = @id AND status = 'draft'
RETURNING *;

-- Asks to start an issue (D62); the first request is kept.
-- name: RequestIssueStart :one
UPDATE issues
SET start_requested_at = coalesce(start_requested_at, now()),
    start_requested_by = coalesce(start_requested_by, @requested_by::text),
    spec_rejected_at = NULL, -- asking again lifts a rejection (D64)
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: MarkIssueScheduled :exec
UPDATE issues SET scheduled_at = now(), updated_at = now() WHERE id = @id;

-- Every issue as the scheduler sees it (D62). busy: an active run, or started by
-- the scheduler in the last five minutes and still waiting for its run: no run
-- was created around or after that start (the mark is written once the start
-- returns, so its run may be a moment older).
-- needs_approval (D64): a label decides, else the project's specApproval.
-- name: ListSchedIssues :many
SELECT i.id, i.key, i.type, i.parent_id, i.status, i.priority, i.number,
       (i.start_requested_at IS NOT NULL)::boolean AS requested,
       (EXISTS (SELECT 1 FROM runs r WHERE r.issue_id = i.id AND r.status IN ('pending', 'running'))
        OR (i.status IN ('ready', 'draft') AND coalesce(i.scheduled_at > now() - interval '5 minutes', false)
            AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.issue_id = i.id AND r.created_at > i.scheduled_at - interval '1 minute')))::boolean AS busy,
       (CASE WHEN 'no-spec-approval' = ANY (i.labels) THEN false
             WHEN 'needs-spec-approval' = ANY (i.labels) THEN true
             ELSE p.spec_approval END)::boolean AS needs_approval,
       (i.spec_rejected_at IS NOT NULL)::boolean AS spec_rejected,
       (i.spec_proposal IS NOT NULL)::boolean AS has_proposal
FROM issues i JOIN projects p ON p.id = i.project_id;

-- name: ListAllDependencies :many
SELECT issue_id, depends_on_id FROM issue_dependencies;

-- The files an issue's latest change touched (D63).
-- name: SetIssueChangedPaths :exec
UPDATE issues SET changed_paths = @changed_paths, updated_at = now() WHERE id = @id;

-- Issues with the paths they touch, for overlap warnings (D63); finished ones are left out.
-- name: ListPathsIssues :many
SELECT id, key, project_id, status, file_hints, changed_paths FROM issues
WHERE status NOT IN ('merged', 'released', 'canceled');

-- name: SetSpecApproved :one
UPDATE issues
SET spec_approved_at = now(), spec_approved_by = @approved_by, spec_rejected_at = NULL, spec_feedback = '', updated_at = now()
WHERE id = @id
RETURNING *;

-- name: RejectSpec :one
UPDATE issues
SET spec_rejected_at = now(), spec_feedback = @feedback, spec_proposal = NULL, updated_at = now()
WHERE id = @id AND status = 'draft'
RETURNING *;

-- The refiner's result for a refining issue (D66).
-- name: SetSpecProposal :one
UPDATE issues SET spec_proposal = @spec_proposal, updated_at = now()
WHERE id = @id AND status = 'refining'
RETURNING *;

-- Approving a refined spec makes it the issue's spec (D66).
-- name: ApplySpecProposal :one
UPDATE issues
SET spec = @spec, acceptance_criteria = @acceptance_criteria, file_hints = @file_hints,
    spec_proposal = NULL, updated_at = now()
WHERE id = @id
RETURNING *;
