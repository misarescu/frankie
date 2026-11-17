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

- Spec approval (D64) is opt-in: a project sets `specApproval: required` in `.factory/config.yaml` (synced into `projects.spec_approval`), and the labels `needs-spec-approval` / `no-spec-approval` override it per issue. Synced issues start as `draft` and follow the source's text while they are drafts. When someone asks to start one, the scheduler makes a draft that needs no approval `ready` itself; one that needs approval is refined first (D66): the refiner runs as a Run of the issue (`draft` -> `refining`), and its result waits on the issue (`spec_proposal`) in the inbox until a person approves it, which makes it the issue's spec, acceptance criteria and file hints (`refining` -> `ready`, `spec_approved_at`/`by`), or rejects it with feedback (`refining` -> `draft`, `spec_feedback`; the rejection holds the draft until its text changes or someone asks to start it again, and the next refinement gets the feedback). A refinement without a usable result returns the issue to `draft`, held the same way, unless it failed before its agent started (image build, pod): then the draft stays requested and is refined again, until three in a row have failed that way (D79).
- `refining`: optional refinement agent rewrites the ticket into acceptance criteria and asks questions. An issue becomes `ready` only after refinement/approval (spec approval is the first human gate; it happens before the workflow starts).
- `running`: a Run is active. `awaiting_human`: a `human` gate or ask-human tool is blocking. The issue moves there while a gate or question inside its run waits and back to `running` once answered (D91), and also when a run without a merge queue ends with a change request waiting to be merged.
- `queued`: in the repo merge queue. `merged`: on `main`, merged by the merge queue (D88) or by a person on the provider (D61). `released`: included in a Release that reached the final stage.

Allowed transitions (enforced in `internal/domain`; anything not listed is rejected):

| From | To |
|---|---|
| `draft` | `refining`, `ready` (approved without refinement) |
| `refining` | `ready`, `draft` (refined spec rejected, or refinement failed, D66) |
| `ready` | `running` |
| `running` | `awaiting_human`, `queued` |
| `awaiting_human` | `running`, `merged` (a person merged the change request on the provider, D61) |
| `queued` | `merged`, `running` (merge queue sends a conflict back to the coder) |
| `merged` | `released` |
| any state before `merged` | `failed`, `canceled` |
| `failed` | `ready` (re-arm for a new Run), `canceled` (a person gives up on it, D70) |
| `draft`, `ready` of a **parent** | `merged` (a person completes it, D70; not a generic transition: only `domain.CheckComplete` allows it) |

`merged` is not failed or canceled: the code is already on `main`; a bad release produces a new bug issue instead. `released` and `canceled` are terminal.

Closing (D70): parents do not run, so they stay `draft` (or `ready`) while their sub-issues work, and stay open by default because issues may be added later; the issue page shows their progress. A person **completes** a parent once every sub-issue is finished (`merged`, `released` or `canceled`) and at least one was not canceled: it moves to `merged`, which also counts it done for its dependents. A person **cancels** any unfinished issue with a reason: it and every unfinished issue under it move to `canceled`; finished ones keep their status. Canceling is refused while a run under the issue is in progress: end it first (abort its agent step or decide its gate). Both record `closed_at`, `closed_by` and (for a cancel) `close_reason`, with a `state_change` event per issue.

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
- `Release`: `id, project_id, repo, sha, issue_ids[], created_at, finished_at, status (pending|in_progress|succeeded|failed|rolled_back)`. One per project, repo and commit (D110).
- `Stage`: `name, order, driver, driver_config, verify, gate?` (stages are project configuration).
- `Deployment`: `id, release_id, stage, driver, driver_state jsonb, status, reason, started_at, finished_at`. One per release and stage, with the release's statuses.

Implemented in the store (D110): a release contains the project's `merged` issues whose change request in its repo merged no later than the one that made its commit, in merge order (`change_requests.merge_sha`, `merged_at`); a commit no recorded merge made contains every merged issue. An issue is in every release made until one containing it succeeds, which moves it to `released`; a failed or rolled-back release leaves its issues `merged`, so the next release contains them again. Releases and deployments move `pending > in_progress > succeeded | failed | rolled_back` (also `pending > failed` and `failed > rolled_back`). A parent completed by a person (D70) has no change request and is in no release.

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
    // Merge merges only if the change request still points to in.HeadSHA (D85).
    Merge(ctx context.Context, id CRID, in MergeRequest) (mergeSHA string, err error)
    Close(ctx context.Context, id CRID, comment string) error
    CommitStatus(ctx context.Context, repo RepoRef, sha string) (CommitStatus, error)
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

Issues carry a start request (`start_requested_at`, `start_requested_by`, D62), which the scheduler reads; intake never starts issues. `file_hints[]` come from the paths the issue's text names in backticks while the text follows the source, and `changed_paths[]` are the files its latest change touched (D63). Since D60 intake syncs trees: the labeled issues with their open sub-issues and their open same-repo blockers, with parents and dependencies, applied atomically per project. The source is authoritative for the links of the issues it reports and for a draft's text; refined or started issues keep the factory's copy. Intake uses the narrower provider-neutral `IssueSource` methods `ListLabeledIssues`, `SubIssues` and `BlockedBy` (`domain.ExternalIssue`) so far. The first write-back (D71) uses `Get`, `UpdateStatus` and `Comment`: when a person completes or cancels an issue (D70), the control plane comments on its provider issue naming who closed it and why, and closes it as completed or not planned.

The merge queue's provider calls (D85) are implemented by the GitHub connector as `MergeChangeRequest`, `CloseChangeRequest` and `CommitStatus`, with the provider-neutral `domain.MergeRequest` and `domain.CommitStatus`.

`ReleaseDriver` is specified in `06-delivery.md`.

## Postgres conventions

- UUIDv7 primary keys generated by the application, `created_at/updated_at` on all tables (append-only tables such as `events` have only `created_at`), soft-delete only where UI needs undelete.
- Migrations are forward-only and run by the control plane at startup behind an advisory lock. Atlas also writes a `-- +goose Down` section; it is never executed.
- Columns arrive with the milestone that needs them; `internal/store/schema.sql` is the current schema.
- Event tables partitioned by month once volume warrants; large payloads (full transcripts) go to object storage with a reference (concrete profile: a PVC directory).
