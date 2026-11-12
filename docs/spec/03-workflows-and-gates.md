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

### Step kinds

| Kind | Fields | Behavior |
|---|---|---|
| agent | `id, agent{role, harness, model}, inputs?, timeout?, budget?` | Launch pod, run role via the harness, push result to the issue's `agent/*` branch. |
| gate | `id, gate{type, ...}` | See below. |
| parallel | list of steps, optional trailing `gate` | Run branches concurrently; gate evaluates after all finish. |
| action | `id, action: <name>` | Built-in control plane action, e.g. `merge-queue.enqueue`, `release.create`, `issue.comment`. |

### Gates

One concept, three types:

| Type | Evaluation | Notes |
|---|---|---|
| `command` | Run commands in a **fresh pod** from the pushed commit with the project's toolchain image. Pass = all exit 0. | Deterministic gates always run before LLM gates. Agent-reported results are never accepted. |
| `agent-verdict` | A reviewer agent returns JSON validated against a schema (`verdict: pass|fail`, `findings[]` with severity, file, line, message). | `require: all|any|majority` for multiple reviewers. |
| `human` | UI/CLI approval via Signal. | `timeout`, `onTimeout: fail|escalate|extend`. Used for spec approval, protected paths, production. |

Every gate has:

- `policy: blocking | advisory`: advisory results are recorded and shown but do not stop the run.
- `onFail`: `{ goto: <step-id>, maxLoops: N }`, `fail`, or `escalate` (route to a human gate).

### Built-in protections

Regardless of the definition, the interpreter forces a human gate when a change touches **protected paths** or removes/skips tests (see `05-sandbox-and-toolchains.md`). A workflow author cannot disable this.

### Validation (at save time)

- All `goto` targets exist and are earlier steps; each loop has `maxLoops`.
- Roles exist and are pinned to a version.
- Harness/model compatibility (e.g. Claude Code requires an Anthropic-style API route; opencode and pi are multi-provider).
- Referenced release driver capabilities (rollback, progressive) are supported by the chosen driver.
- Gate commands reference tasks that exist in the repo's `mise.toml` (warning, not error, if the repo is not yet cloned).

## Roles

A role is a versioned file in `roles/` (or the project repo's `.factory/roles/`):

```yaml
name: code-reviewer
version: 3
system_prompt_file: prompts/code-reviewer.md
skills: [go-review, helm-review]
allowed_tools: [read, grep, glob, bash:readonly]
mcp_servers: []
default_model: anthropic/sonnet
output_contract:
  schema: schemas/review-verdict.json      # required for agent-verdict roles
```

Initial roles: `refiner`, `coder` (Go, React variants), `e2e-test-writer`, `code-reviewer`, `security-reviewer`, `helm-reviewer`, `release-agent`, `flag-cleanup`. Each role's prompt tells it about the repo's `CLAUDE.md`/`AGENTS.md`, ADRs and coding standards, which live in the repo and are read every run. Agents may propose changes to those documents through a normal change request.

## Run-level semantics

- **Scheduling.** "Start" on one issue creates a Run. "Start many" asks the scheduler to run all ready issues respecting the DAG and concurrency limits (`maxConcurrentIssues`). Independent issues run in parallel.
- **Overlap warning.** Before launching parallel issues, the scheduler compares `file_hints` and recent diffs by package; likely overlap produces a UI warning (and optionally serialization).
- **Budgets.** Per-step, per-issue and per-release token/cost budgets enforced at the LLM gateway. Exceeding a budget aborts the step and escalates.
- **Loop counts** are recorded per issue. High loop counts usually indicate a weak spec and surface in metrics.
- **Concurrency of reviewers** is configurable; on the laptop profile reviewers run sequentially.
