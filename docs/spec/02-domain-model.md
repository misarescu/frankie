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
`id, name, key_prefix, repos[], issue_source, git_provider, workflow_ref, stages[], features{flags: bool}, toolchain_ref, budgets, created_at`. Per-project config lives in `.factory/config.yaml` in the repo and is synced to the DB; the repo copy is authoritative. `key_prefix` (D22) is 2-10 lowercase letters or digits starting with a letter, unique, and immutable.

### Issue
`id, project_id, number, key, parent_id?, type (epic|story|task|bug), title, spec (markdown), acceptance_criteria[], flag_name?, depends_on[], status, priority/order, source_ref? (e.g. GitHub issue URL), file_hints[]? (for overlap warnings), created_by, labels`.

- `number` is allocated per project starting at 1; `key` is `<key_prefix>-<number>` (e.g. `frk-42`), globally unique (D22). Agent branches are `agent/<key>/<slug>`.
- `parent_id` (D57): an epic is a root, a story sits under an epic, a task or bug under an epic or a story; any type but epic may also be a root. The parent is in the same project.
- `depends_on` (D57) is stored as `issue_dependencies` rows, between issues of the same project. An issue waits for its own dependencies and for those of every ancestor; a parent is finished only once all its children are. A change after which an issue would wait for itself, through dependencies and the hierarchy together, is rejected.
- `flag_name` is required for type `story|task` features when flags are enabled; empty for bug fixes.
- Acceptance criteria are first-class: the e2e test agent writes tests from them and tags each test with the issue key.

**Issue status**

```
draft -> refining -> ready -> running -> awaiting_human -> queued -> merged -> released
                       \________________ failed / canceled (from any non-terminal state)
```

- `refining`: optional refinement agent rewrites the ticket into acceptance criteria and asks questions. An issue becomes `ready` only after refinement/approval (spec approval is the first human gate; it happens before the workflow starts).
- `running`: a Run is active. `awaiting_human`: a `human` gate or ask-human tool is blocking.
- `queued`: in the repo merge queue. `merged`: on `main`; until the merge queue exists (M6), an issue reaches it when a person merges its change request on the provider (D61). `released`: included in a Release that reached the final stage.

Allowed transitions (enforced in `internal/domain`; anything not listed is rejected):

| From | To |
|---|---|
| `draft` | `refining`, `ready` (approved without refinement) |
| `refining` | `ready` |
| `ready` | `running` |
| `running` | `awaiting_human`, `queued` |
| `awaiting_human` | `running`, `merged` (a person merged the change request on the provider, D61) |
| `queued` | `merged`, `running` (merge queue sends a conflict back to the coder) |
| `merged` | `released` |
| any state before `merged` | `failed`, `canceled` |
| `failed` | `ready` (re-arm for a new Run) |

`merged` is not failed or canceled: the code is already on `main`; a bad release produces a new bug issue instead. `released` and `canceled` are terminal.

### Run / StepRun / AgentSession
Run and StepRun share one status machine: `pending -> running -> succeeded | failed | canceled`, plus `pending -> failed | canceled`. Entering `running` stamps `started_at`; a terminal status stamps `finished_at`. An issue has at most one active (`pending` or `running`) Run, enforced by a partial unique index.

An agent StepRun records its sessions' model usage (D53): `cost_usd` (the LLM gateway's spend for the session's virtual key), `input_tokens` and `output_tokens` (the harness's session totals); a retried session's attempts each add theirs. A Run's and an Issue's usage are the sums of their StepRuns'.

Every status change (Issue, Run, StepRun) is a compare-and-set on the expected current status and writes a `state_change` Event in the same transaction (FR-WF-7). Repeating a transition that already happened is a no-op success, so retried Activities stay idempotent.

- `Run`: `id, issue_id, workflow_snapshot (jsonb), workflow_version, temporal_workflow_id, status, loop_counts, cost, tokens, started_at, finished_at`.
- `StepRun`: `id, run_id, step_id, attempt, kind (agent|gate|action|parallel), status, pod_name?, commit_sha?, started_at, finished_at`.
- `AgentSession`: `id, step_run_id, harness, model, role_version, event_log_ref, tokens_in/out, cost`.
- `GateResult`: `id, step_run_id, gate_type, policy (blocking|advisory), verdict (pass|fail|escalated), details jsonb`.

### ChangeRequest
`id, issue_id, repo_id, provider_ref, branch (agent/<issue-key>/...), head_sha, status, preview_env?`.

### MergeQueueEntry
`id, change_request_id, position, state (waiting|rebasing|testing|merging|returned|merged), reason?`.

### Release / Stage / Deployment
- `Release`: `id, project_id, sha, issue_ids[], created_at, status (pending|in_progress|succeeded|failed|rolled_back)`.
- `Stage`: `name, order, driver, driver_config, verify, gate?` (stages are project configuration).
- `Deployment`: `id, release_id, stage, driver_state jsonb, status, started_at, finished_at`.

### Flag (optional)
`key, project_id, issue_id, rollout_state (off|internal|test_pool|percentage|full), percentage?, at_full_since?`. Definitions are files in the GitOps repo (flagd); this table is an index for UI and cleanup scheduling.

### Event
Append-only: `id, seq, run_id?, step_run_id?, session_id?, ts, type, payload jsonb`. `seq` is a database identity giving a strict total order; streaming and replay page by `seq` (UUIDv7 IDs generated by several processes are only roughly ordered). Types include assistant message, tool call, file diff, command output, token usage, gate result, state change.

## Connector interfaces (Go)

```go
type GitProvider interface {
    CloneURL(ctx context.Context, repo RepoRef) (url string, err error)
    // UpstreamCredential is used only by the control plane git proxy (D21); it never reaches a pod.
    UpstreamCredential(ctx context.Context, repo RepoRef) (Token, error)
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

Intake (D27): the control plane polls `List` (with `since`) and, once webhooks are reachable, also accepts `Webhook` deliveries; both go through one idempotent intake keyed by `source_ref`, so a delivery and a poll that see the same issue create it once.

Issues carry a start request (`start_requested_at`, `start_requested_by`, D62), which the scheduler reads; intake never starts issues. Since D60 intake syncs trees: the labeled issues with their open sub-issues and their open same-repo blockers, with parents and dependencies, applied atomically per project. The source is authoritative for the links of the issues it reports and for a draft's text; refined or started issues keep the factory's copy. Intake uses the narrower provider-neutral `IssueSource` methods `ListLabeledIssues`, `SubIssues` and `BlockedBy` (`domain.ExternalIssue`) so far.

`ReleaseDriver` is specified in `06-delivery.md`.

## Postgres conventions

- UUIDv7 primary keys generated by the application, `created_at/updated_at` on all tables (append-only tables such as `events` have only `created_at`), soft-delete only where UI needs undelete.
- Migrations are forward-only and run by the control plane at startup behind an advisory lock. Atlas also writes a `-- +goose Down` section; it is never executed.
- Columns arrive with the milestone that needs them; `internal/store/schema.sql` is the current schema.
- Event tables partitioned by month once volume warrants; large payloads (full transcripts) go to object storage with a reference (concrete profile: a PVC directory).
