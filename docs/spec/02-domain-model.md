# 02 - Domain model

The platform keeps its **own issue store** even when Jira or GitHub Issues is the source, because ordering, dependencies and workflow state live here. External sources sync into it; a native board is just another source.

## Entities

```
Project 1--* Repo
Project 1--* Issue            (self-referential: parent_id for epic > story > task)
Issue   *--* Issue            (depends_on: DAG)
Issue   1--* Run              (a retry/rerun creates a new Run)
Run     1--* StepRun          1--* AgentSession   (harness session, events)
StepRun 0..* GateResult
Issue   0..1 ChangeRequest    (neutral PR/MR)
Repo    1--1 MergeQueue       1--* MergeQueueEntry (one per ChangeRequest)
Project 1--* Release          (git SHA + set of Issues)
Release 1--* Deployment       (one per Stage)
Project 1--* Stage            (ordered; driver, verify, gate)
Project 1--* Flag             (only if feature flags enabled)
Project 1--* WorkflowDefinition (versioned)   Role (versioned)
```

### Project
`id, name, repos[], issue_source, git_provider, workflow_ref, stages[], features{flags: bool}, toolchain_ref, budgets, created_at`. Per-project config lives in `.factory/config.yaml` in the repo and is synced to the DB; the repo copy is authoritative.

### Issue
`id, project_id, parent_id?, type (epic|story|task|bug), title, spec (markdown), acceptance_criteria[], flag_name?, depends_on[], status, priority/order, source_ref? (e.g. GitHub issue URL), file_hints[]? (for overlap warnings), created_by, labels`.

- `flag_name` is required for type `story|task` features when flags are enabled; empty for bug fixes.
- Acceptance criteria are first-class: the e2e test agent writes tests from them and tags each test with the issue ID.

**Issue status**

```
draft -> refining -> ready -> running -> awaiting_human -> queued -> merged -> released
                       \________________ failed / canceled (from any non-terminal state)
```

- `refining`: optional refinement agent rewrites the ticket into acceptance criteria and asks questions. An issue becomes `ready` only after refinement/approval (spec approval is the first human gate; it happens before the workflow starts).
- `running`: a Run is active. `awaiting_human`: a `human` gate or ask-human tool is blocking.
- `queued`: in the repo merge queue. `merged`: on `main`. `released`: included in a Release that reached the final stage.

### Run / StepRun / AgentSession
- `Run`: `id, issue_id, workflow_snapshot (jsonb), workflow_version, temporal_workflow_id, status, loop_counts, cost, tokens, started_at, finished_at`.
- `StepRun`: `id, run_id, step_id, attempt, kind (agent|gate|action|parallel), status, pod_name?, commit_sha?, started_at, finished_at`.
- `AgentSession`: `id, step_run_id, harness, model, role_version, event_log_ref, tokens_in/out, cost`.
- `GateResult`: `id, step_run_id, gate_type, policy (blocking|advisory), verdict (pass|fail|escalated), details jsonb`.

### ChangeRequest
`id, issue_id, repo_id, provider_ref, branch (agent/<issue-id>/...), head_sha, status, preview_env?`.

### MergeQueueEntry
`id, change_request_id, position, state (waiting|rebasing|testing|merging|returned|merged), reason?`.

### Release / Stage / Deployment
- `Release`: `id, project_id, sha, issue_ids[], created_at, status (pending|in_progress|succeeded|failed|rolled_back)`.
- `Stage`: `name, order, driver, driver_config, verify, gate?` (stages are project configuration).
- `Deployment`: `id, release_id, stage, driver_state jsonb, status, started_at, finished_at`.

### Flag (optional)
`key, project_id, issue_id, rollout_state (off|internal|test_pool|percentage|full), percentage?, at_full_since?`. Definitions are files in the GitOps repo (flagd); this table is an index for UI and cleanup scheduling.

### Event
Append-only: `id, run_id?, step_run_id?, session_id?, ts, type, payload jsonb`. Types include assistant message, tool call, file diff, command output, token usage, gate result, state change.

## Connector interfaces (Go)

```go
type GitProvider interface {
    CloneURL(ctx context.Context, repo RepoRef) (url string, err error)
    ScopedPushToken(ctx context.Context, repo RepoRef, branchPrefix string, ttl time.Duration) (Token, error)
    OpenChangeRequest(ctx context.Context, in OpenCR) (ChangeRequest, error)
    UpdateChangeRequest(ctx context.Context, id CRID, in UpdateCR) error
    Merge(ctx context.Context, id CRID) error
    CommitStatus(ctx context.Context, repo RepoRef, sha string) (Status, error)
}

type IssueSource interface {
    List(ctx context.Context, project ProjectRef, since time.Time) ([]ExternalIssue, error)
    Get(ctx context.Context, ref string) (ExternalIssue, error)
    UpdateStatus(ctx context.Context, ref string, status IssueStatus) error
    Comment(ctx context.Context, ref string, body string) error
    Webhook(ctx context.Context, r *http.Request) ([]SourceEvent, error)
}
```

`ReleaseDriver` is specified in `06-delivery.md`.

## Postgres conventions

- UUIDv7 primary keys, `created_at/updated_at` on all tables, soft-delete only where UI needs undelete.
- Migrations are forward-only and run by the control plane at startup behind an advisory lock.
- Event tables partitioned by month once volume warrants; large payloads (full transcripts) go to object storage with a reference (laptop profile: a PVC directory).
