# 03 - Workflows, roles and gates

## Generic interpreter

One Temporal workflow, `Interpret`, executes a declarative workflow definition authored by the engineer. There is no hardcoded workflow per factory flavor.

Requirements:

- **FR-WF-1** The definition is snapshotted into the workflow input at start. Edits to YAML affect only new runs. This avoids Temporal determinism and versioning problems.
- **FR-WF-2** Every step is an Activity. Agent steps launch a pod and heartbeat while it runs; cancellation aborts the pod.
- **FR-WF-3** Human gates are Temporal Signals with a configurable timeout and an escalation policy.
- **FR-WF-4** Loops are bounded (`maxLoops`); exceeding the bound escalates (default: human gate).
- **FR-WF-5** Definitions are validated when saved (see "Validation").
- **FR-WF-6** Changes to interpreter code use Temporal versioning and ship with replay tests against recorded histories.
- **FR-WF-7** Every step transition emits an Event and updates `StepRun`.

## Definition schema

```yaml
name: default-build
version: 1
steps:
  - parallel:
      - id: implement
        agent: { role: coder, harness: claude-code, model: anthropic/opus }
      - id: e2e-tests
        agent: { role: e2e-test-writer, harness: opencode, model: anthropic/sonnet }
        inputs: [spec, acceptance_criteria]     # deliberately NOT the coder's diff
  - id: checks
    gate:
      type: command
      run: ["mise run lint", "mise run test"]
      policy: blocking
      onFail: { goto: implement, maxLoops: 3 }
  - id: review
    parallel:
      - agent: { role: security-reviewer, harness: opencode, model: openai/gpt-x }
      - agent: { role: code-reviewer,     harness: pi,       model: anthropic/sonnet }
    gate:
      type: agent-verdict
      require: all
      policy: blocking
      onFail: { goto: implement, maxLoops: 2 }
  - id: enqueue
    action: merge-queue.enqueue
```

Notes versus the earliest sketch: `implement` and `e2e-tests` are in a `parallel` group because the e2e test agent runs in parallel with the coder (D9). Model names above are placeholders; real refs are resolved against provider profiles.

### Where definitions come from (D39)

The platform ships built-in definitions, embedded from `workflows/` (`default-build` is the default). A project selects one with `workflow: <name>` in `.factory/config.yaml`, or adds its own as `.factory/workflows/<name>.yaml`. The definition is read from the base branch head when a run starts, validated, and snapshotted into the workflow input (FR-WF-1); definitions in the repo are protected paths. Until later milestones, the built-in `default-build` has no `e2e-tests` branch (M7), one Claude Code reviewer (M4) and ends with `change-request.open` (M6).

Parsing fills in every default, so the snapshot is explicit: `harness: claude-code`; all inputs; `policy: blocking`; `onFail: fail` for blocking gates; `run: ["mise run lint", "mise run test"]` for command gates; `require: all`; human gates `timeout: 72h, onTimeout: fail`.

### Step kinds

| Kind | Fields | Behavior |
|---|---|---|
| agent | `id, agent{role, harness, model}, inputs?, timeout?, budgetUSD?` | Launch pod, run role via the harness, push result to the issue's `agent/*` branch. |
| gate | `id, gate{type, ...}` | See below. |
| parallel | list of agent steps, optional trailing `gate`; `id` required when gated | Run branches concurrently; gate evaluates after all finish. A branch without an `id` is named after its role. |
| action | `id, action: <name>` | Built-in control plane action. M3 implements `change-request.open`; `merge-queue.enqueue` (M6), `release.create` (M7) and `issue.comment` are rejected until they exist. |

- `role` is a role name, optionally pinned as `name@version`.
- `model` is a model ref `<provider>/<model id>` (the id may contain slashes, as OpenRouter's `openrouter/openai/gpt-5.6-terra`); empty means the role's default model, then the platform's. A harness may use a provider only if it speaks the API that provider serves (D48): Claude Code reaches the `anthropic` provider only; opencode and pi reach every provider.
- `inputs` are `spec`, `acceptance_criteria`, `diff` and `feedback` (the findings of the gate that looped back). Until the native issue store (M5) separates them, `spec` and `acceptance_criteria` both come from the issue body. Every agent step, whatever its inputs, also gets the issue's answered ask-human questions (D56).
- A `goto` to a branch of an earlier parallel group re-runs only that branch, then continues with the step after the group.

### Gates

One concept, three types:

| Type | Evaluation | Notes |
|---|---|---|
| `command` | Run commands in a **fresh pod** from the pushed commit with the project's toolchain image. Pass = all exit 0, as recorded by the kubelet per command (D33). | Deterministic gates always run before LLM gates. Agent-reported results are never accepted. |
| `agent-verdict` | A reviewer agent returns JSON validated against a schema (`verdict: pass|fail`, `findings[]` with severity, file, line, message). | `require: all|any|majority` for multiple reviewers. Only on a parallel group (its branches are the reviewers), and only after a `command` gate. |
| `human` | UI/CLI approval via Signal. | `timeout`, `onTimeout: fail|extend` (`escalate` needs a notification channel; not yet). Always blocking; a rejection fails the run. Used for spec approval, protected paths, production. |

Every gate has:

- `policy: blocking | advisory`: advisory results are recorded and shown but do not stop the run, so advisory gates have no `onFail`.
- `onFail`: `{ goto: <step-id>, maxLoops: N }`, `fail`, or `escalate` (route to a human gate). `goto` targets an earlier agent step, parallel group or branch, never the gate's own group; `maxLoops` is 1 to 10.

### Built-in protections

Regardless of the definition, the interpreter forces a human gate when a change touches **protected paths** or removes/skips tests (see `05-sandbox-and-toolchains.md`). A workflow author cannot disable this. The gate comes after the definition's last step, so after `change-request.open` (D44).

### Validation (at save time)

Structural checks are in `internal/config` (`ParseWorkflow`); the cross-checks against roles and the repo arrive with them.

- All `goto` targets exist and are earlier steps; each loop has `maxLoops`.
- Step IDs are unique (branches included); each step is exactly one kind; gate fields match the gate type.
- Harnesses and actions exist and are implemented.
- Roles exist, a pinned version matches, a role's default model suits the step's harness, the harness provides every tool the role allows (pi has no `web-fetch`, D50), and verdict roles sit exactly in `agent-verdict` groups (`config.ResolveRoles`).
- Harness/model compatibility (D48): the provider of each model ref is known and serves an API the step's harness speaks (Claude Code: Anthropic; opencode and pi: Anthropic and OpenAI-compatible). When a run starts, every agent step's effective model (the step's, else the role's default, else the platform's) must also be served by one of the deployment's provider profiles (`config.CheckModels`); otherwise the run fails with the reason.
- Referenced release driver capabilities (rollback, progressive) are supported by the chosen driver.
- Gate commands reference tasks that exist in the repo's `mise.toml` (warning, not error, if the repo is not yet cloned).

## Roles

A role is a versioned file in `roles/` (or the project repo's `.factory/roles/`):

```yaml
name: code-reviewer
version: 3
system_prompt_file: prompts/code-reviewer.md
skills: [go-review, helm-review]              # not available yet
allowed_tools: [read, grep, glob, bash:readonly]
mcp_servers: []                                # not available yet
default_model: anthropic/claude-sonnet-5-5
output_contract:
  schema: review-verdict                       # required for agent-verdict roles
```

Roles are parsed by `internal/config` (`ParseRole`); the built-ins are embedded from `roles/` (D40). Paths in a role file are relative to its directory and may not leave it. The system prompt is read when the role is loaded and the resolved role (prompt text included) is snapshotted into the run's input with the workflow, so a running workflow never sees a later prompt. A pinned ref (`code-reviewer@3`) must match the role's `version`; an unpinned one takes the current version, recorded in the snapshot.

- `allowed_tools` uses harness-neutral names: `read`, `grep`, `glob`, `edit`, `write`, `bash`, `bash:readonly`, `web-fetch`. Empty means the harness's defaults.
- `output_contract.schema` names a contract the platform knows, not a file: the gate must understand the result, so a repo cannot define its own. The only one is `review-verdict` (`domain.ReviewVerdict`, JSON Schema in `roles/schemas/review-verdict.json`): `{verdict: pass|fail, summary?, findings: [{severity: blocker|major|minor|info, file?, line?, message}]}`, at most 100 findings, and a passing verdict carries no blocker. The platform appends the contract's instructions and schema to the role's system prompt.
- A role returning a verdict runs only as a branch of a parallel group judged by an `agent-verdict` gate, and such a group's branches all return verdicts.

M3 ships `coder` and `code-reviewer`.

Initial roles: `refiner`, `coder` (Go, React variants), `e2e-test-writer`, `code-reviewer`, `security-reviewer`, `helm-reviewer`, `release-agent`, `flag-cleanup`. Each role's prompt tells it about the repo's `CLAUDE.md`/`AGENTS.md`, ADRs and coding standards, which live in the repo and are read every run. Agents may propose changes to those documents through a normal change request.

## Run-level semantics

- **Scheduling.** "Start" on one issue creates a Run. "Start many" asks the scheduler to run all ready issues respecting the DAG and concurrency limits (`maxConcurrentIssues`). Independent issues run in parallel. Implemented (D62): `POST /api/issues/start` with issue keys records a start request on each (a request on a parent covers everything under it, including children synced later); every 5 s the scheduler starts each requested leaf that is `ready`, not busy, and whose own and inherited dependencies are done (a leaf when `merged` or `released`, a parent when all its non-canceled children are), by priority and then number, while fewer than `maxConcurrentIssues` issues hold an active run (a run waiting on a human gate holds its slot). The reply shows what starts and what waits, with why. `POST /api/issues/{key}/retry` re-arms a failed issue and requests it. Intake only syncs; it starts nothing.
- **Overlap warning.** Before launching parallel issues, the scheduler compares `file_hints` and recent diffs by package; likely overlap produces a UI warning (and optionally serialization). Implemented (D63) as a warning: an issue's paths are its `file_hints` (paths its text names in backticks, until refinement sets them) and `changed_paths` (the files its latest change touched, recorded when the guard diffs it); two issues overlap when a directory of one equals or contains a directory of the other. Issues about to start are compared with the in-flight issues of their project (running, awaiting a person, queued) and with each other. The start reply and the issue page list the overlaps and the scheduler logs them; nothing is serialized yet.
- **Budgets.** Per-step, per-issue and per-release token/cost budgets enforced at the LLM gateway. Exceeding a budget aborts the step and escalates. Implemented (D53): per step (`budgetUSD`, default `controlPlane.agent.budgetUSD`) and per issue (`budgets.issueUSD` in `.factory/config.yaml`, default `controlPlane.agent.issueBudgetUSD`), across all the issue's runs; each session's virtual key is capped at the lower of the step's budget and what is left of the issue's. A used-up budget fails the step, and so the run, naming the budget; escalating to a person who can grant more is not built yet, and per-release budgets come with releases (M7).
- **Loop counts** are recorded per issue. High loop counts usually indicate a weak spec and surface in metrics.
- **Concurrency of reviewers** is configurable; on the concrete profile reviewers run sequentially.

## Implementation

### IssueRun: the M1/M2 workflow

`internal/workflow.IssueRun` ran every issue the same way until `Interpret` took over (D41, D42); intake no longer starts it: `StartRun` (Run + `implement` StepRun, issue `ready -> running`), `EnsureToolchainImage`, `LaunchAgentPod` (Kata pod from `internal/sandbox`), `RunAgentSession` (mints the git proxy token and gateway virtual key, passes them to the runner over gRPC so they never enter the pod spec or the workflow history, persists every event, heartbeats the last stored `seq` so a retry resumes the stream), `DeleteAgentPod` (always, on a disconnected context), `RunCommandGate`, `OpenChangeRequest` (idempotent), `FinishRun` (issue `awaiting_human` on success, `failed` otherwise). Workflow ID `issue-<key>` with "use existing" on conflict, so intake polls never start a second concurrent run. `IssueRun` stays registered, with its replay fixtures, until its in-flight runs drain.

### Interpret (D41, D42, D43, D44)

`internal/workflow.Interpret` is a new workflow type rather than a changed `IssueRun`, so it needed no `workflow.GetVersion`. Its input is the issue plus a `snapshot.Snapshot`: the definition, the resolved roles (prompt text included), where the definition came from, and the base branch commit they were read at. The control plane's starter resolves the snapshot before starting the workflow (`internal/snapshot`), so the definition is in the workflow input itself (invariant 4). If the project's configuration is invalid, or selects something the interpreter cannot run yet (`workflow.CheckSupported`), the snapshot carries the error instead and the run fails with it, visibly, before any step. A failure to read the repo is retried on the next intake poll.

The interpreter walks the top-level steps in order:

- **Agent step**: `StartStep` creates a StepRun per execution (`attempt` counts executions of that step in the run). `EnsureToolchainImage`, `LaunchAgentPod`, `RunAgentSession` and `DeleteAgentPod` are IssueRun's Activities, given the step's role prompt, gateway model (the model ref without its provider), budget, timeout and inputs through `StartRunResult.Step`. `FinishStep` ends the StepRun. The step continues the issue's agent branch (D36). While the session runs, the workflow relays people's steers and aborts for this StepRun to its runner (D52); an abort fails the run with who aborted it and why.
- **Command gate**: `RunGate` runs the definition's commands, with its timeout, from the last pushed commit, sharing `RunCommandGate`'s implementation. Its outcome carries `Feedback`: each failed command with the end of its output.
- **Parallel group**: the branches run one after another, in definition order, on every profile for now (D42). A branch whose role has an output contract is a **reviewer**: it runs on the last pushed commit with a read-only git token and a read-only workspace (the runner never commits or pushes it), the role's `allowed_tools`, and no feedback input; its final message is its result. Other branches are coding steps as above. A group's trailing command gate runs like a standalone one.
- **Agent-verdict gate**: `RunVerdictGate` parses each reviewer's final message against the `review-verdict` contract (a code fence or a sentence around the JSON object is tolerated), checks that each reviewer read the commit under review, combines the verdicts with `require`, and records one `gate_results` row whose details list every review (`domain.AgentVerdictDetails`). The feedback for a loop back is the failing reviews' findings, most severe first. A reviewer that returns no valid verdict makes the gate **inconclusive**: the run fails with the reason instead of sending the coder back, since the coder cannot fix the reviewer.
- **onFail**: a failed blocking gate fails the run, escalates, or with `goto` jumps back to the target step after `RecordLoop` stores the gate's loop count on the Run (`runs.loop_counts`). The next coding step that takes the `feedback` input gets the gate's feedback in its prompt. A `goto` to a branch runs only that branch of its group, then continues after the group. A gate that has used its `maxLoops` escalates (FR-WF-4; `workflow.GetVersion("escalate-exhausted-loops")`, runs from before failed there).
- **Human gate** (FR-WF-3): `StartStep`, then `OpenHumanGate` writes an `approvals` row (reason, the last pushed commit, the change request if one is open, expiry) and the run waits for the `human-gate-decision` signal or the timeout. A decision for any other gate is ignored. `CloseHumanGate` records the decision on the approval, a `human` gate result (pass only when approved) and the StepRun's end; the first decision stands. Rejected fails the run; on timeout `onTimeout: fail` fails it and `extend` waits another period (`ExtendHumanGate`). A run canceled while waiting closes the approval as rejected.
- **Protected changes** (invariant 6): once the definition's steps have passed, `DetectProtectedChanges` inspects the provider's diff from the snapshot's base commit to the last push against the snapshot's protected-path patterns (docs/spec/05). Any finding forces a `protected-changes` human gate (72 h, then fail), opened after any change request so the diff can be read there; the definition cannot turn it off. Behind `workflow.GetVersion("protected-changes")`.
- **Escalation**: `onFail: escalate`, or a used-up loop, opens a human gate named `<gate>-escalation` (72 h, then fail) explaining what failed. Approving accepts the change despite the failure and continues after the gate; rejecting fails the run.
- **Action**: `change-request.open` opens (or finds) the change request for the last pushed commit.
- `FinishRun` ends the Run and the issue, without a StepRunID.

Decisions come from the approvals inbox in the web UI (`#/approvals`; it also lists the agents' ask-human questions, docs/spec/04 and D55), through the API: `GET /api/approvals` lists pending approvals, and `POST /api/gates/{step-run-id}/decision` with `{"decision": "approve" | "reject", "comment", "by"}` checks that the approval is pending and signals the run's workflow (202). The workflow is the only writer of approvals; the API only reads them. The endpoint accepts JSON only, so a cross-site form cannot post a decision, and like every write it needs the API token (D58); who decided is still what the client says (open question 7).

Replay fixtures are recorded from `Interpret` with stub Activities on a local Temporal dev server by `internal/workflow/record_test.go` (build tag `record`).

Workflow types live one per file in `internal/workflow`, sharing `TaskQueue` and Activity options from `workflow.go`. Their Activities are in `internal/workflow/activities`, one per file.
