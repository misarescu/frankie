# 04 - Agents, harnesses and models

## Principle

Do not write an agent loop. Wrap existing harnesses headlessly and invest in the control plane. Adding a harness must only touch the runner.

## Runner

A small Go binary in every agent pod, next to the harness. The control plane never speaks harness protocols directly. The contract is `proto/frankie/runner/v1/runner.proto` (event payload schemas are documented there):

```proto
service RunnerService {
  rpc Start(StartRequest) returns (StartResponse);    // harness, model, system prompt, env, timeouts, max turns
  rpc Prompt(PromptRequest) returns (PromptResponse);  // the task; exactly once
  rpc Steer(SteerRequest) returns (SteerResponse);     // further messages while the session runs
  rpc Abort(AbortRequest) returns (AbortResponse);
  rpc Events(EventsRequest) returns (stream EventsResponse); // replay after a seq, then follow live
}
```

Session semantics:

- One session per runner (one pod per step). Follow-up work after a session ends is a new step, not a new prompt.
- The session ends when the harness has answered every message (Prompt plus Steers), on Abort, or on a timeout. The stream always ends with exactly one `done` event: `succeeded`, `failed`, `aborted` or `timeout`.
- Events carry a per-session `seq`; a client that reconnects with the last seen `seq` resumes without gaps or duplicates.
- Timeouts (FR-AG-3): `timeout` is wall-clock for the session; `idle_timeout` is time without any harness output. A long tool run (e.g. a test suite) produces no output until it finishes, so `idle_timeout` must exceed the longest expected tool run.
- Abort and timeouts signal the harness's whole process group (SIGTERM, then SIGKILL after a grace period), so commands it spawned stop too.

Workspace (`internal/runner/workspace`, optional `Workspace` in `StartRequest`): `Start` clones `base_ref` through the git proxy (D21) and checks out the step branch before the harness starts; a failed clone fails `Start`. When the session succeeds, the runner commits what the agent left uncommitted (agent commits are kept), emits one `file_diff` per changed file against the base commit, pushes, and reports `branch`, `commit_sha` and `pushed` on `done`; a failed push turns the session into `failed`. Failed, aborted and timed-out sessions push nothing, so a retry starts from the last good push. The proxy token reaches git only through environment configuration scoped to the clone URL, so it is never written to `.git/config`; the runner pushes to the configured URL rather than `origin` and skips hooks, because the agent controls `.git/config` and `.git/hooks`.

The runner:

1. Clones the repo/branch (fresh clone, no host mounts), checks out or creates the step branch.
2. Writes harness-specific config from the role and provider profile.
3. Starts the harness and translates its protocol to the normalized `Event` stream.
4. On completion, commits, pushes to `agent/<issue-id>/*`, and reports the commit SHA.

## Harness adapters

| Harness | Headless interface | Notes |
|---|---|---|
| Claude Code | stream-JSON over stdin/stdout, or the Agent SDK | Expects an Anthropic-style API route (via the gateway). |
| opencode | `opencode serve` (OpenAPI REST + SSE) or native ACP (`opencode acp`) | Multi-provider. |
| pi | `pi --mode rpc` JSONL over stdin/stdout, with `steer` and `follow_up` commands | Multi-provider; supports mid-run messages. |

**ACP (Agent Client Protocol)** is a common JSON-RPC protocol for driving coding agents. opencode supports it natively; Claude Code and pi work through adapters (a Go adapter, `acp-adapter`, exists). The runner may prefer ACP where available and fall back to the native protocol. Verify adapter maturity before depending on it (open question).

Adapter contract: map to the normalized event types - `assistant_message`, `tool_call`, `tool_result`, `file_diff`, `command_output`, `token_usage`, `error`, `done`.

Claude Code adapter (`internal/runner/claudecode`): runs `claude -p --input-format stream-json --output-format stream-json --verbose --dangerously-skip-permissions` (the pod is the sandbox; the flag is refused as root, matching FR-SB-1). Text blocks become `assistant_message`, `tool_use` becomes `tool_call`, results of the `Bash` tool become `command_output` and other tool results `tool_result` (output capped at 256 KiB per event), and each `result` line becomes a `token_usage` event with session totals (Claude Code reports tokens and turns per answered message but cost for the whole session; the adapter sums the former). The runner closes stdin once every message is answered, which lets Claude Code exit. Verified against Claude Code 2.1.323 with `mise run test:smoke-claude` (spends real usage): a message sent mid-turn is queued and gets its own `result`, and closing stdin after the last one exits cleanly. `file_diff` comes from the runner's own `git diff` at publish time, not from tool inputs.

## Models and providers

- **Provider profiles** hold credentials reference, base URL and limits. They are separate from **model refs** used per step (e.g. `anthropic/opus`). Mixing models inside one workflow is expected.
- Harness/model compatibility is validated when a workflow is saved.
- **LLM gateway (LiteLLM)** sits in front of every provider:
  - real API keys never enter a sandbox; each run receives a short-lived virtual key;
  - budgets per run and per issue are enforced in one place;
  - token and cost data is collected uniformly regardless of harness;
  - enables fallbacks and routing later.

## Human intervention, least to most invasive

1. **Ask-human tool**: an MCP tool the agent calls when stuck. It blocks on a Temporal Signal answered from the UI.
2. **Steer**: inject a message into a running session via the runner (`Steer`). Harnesses without native support get it at the next turn boundary.
3. **Pause/abort** a step via Signal.
4. **Take over**: web terminal into the pod.

## Agent MCP servers

- `ask-human` (above).
- `factory-driver` (optional): scoped release-driver tools. Reversible/harmless tools allowed (e.g. `Status`, beta channel publish). Irreversible tools (stable publish) create a pending action that requires a human gate.
- Project-defined MCP servers per role are allowed but must be declared in the role file and pass the egress allowlist.

## Required behaviors

- **FR-AG-1** Runner restart mid-step must not lose committed work; uncommitted changes are lost and the step retries from the branch.
- **FR-AG-2** Every event is persisted so any run can be replayed as a transcript.
- **FR-AG-3** Agent steps have wall-clock timeouts and idle timeouts in addition to token budgets.
- **FR-AG-4** The role's `output_contract` is validated by the runner; non-conforming output is retried once, then fails the step.
