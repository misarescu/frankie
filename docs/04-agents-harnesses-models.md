# 04 - Agents, harnesses and models

## Principle

Do not write an agent loop. Wrap existing harnesses headlessly and invest in the control plane. Adding a harness must only touch the runner.

## Runner

A small Go binary in every agent pod, next to the harness. The control plane never speaks harness protocols directly.

```proto
service Runner {
  rpc Start(StartRequest) returns (StartResponse);   // role, model ref, repo/branch, env, budgets
  rpc Prompt(PromptRequest) returns (PromptResponse); // initial task or follow-up
  rpc Steer(SteerRequest) returns (SteerResponse);    // inject a message mid-run
  rpc Abort(AbortRequest) returns (AbortResponse);
  rpc Events(EventsRequest) returns (stream Event);   // normalized event stream
}
```

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
