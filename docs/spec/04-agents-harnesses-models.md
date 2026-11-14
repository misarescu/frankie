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

Workspace (`internal/runner/workspace`, optional `Workspace` in `StartRequest`): `Start` clones `base_ref` through the git proxy (D21) and checks out the step branch before the harness starts; a failed clone fails `Start`. When the session succeeds, the runner commits what the agent left uncommitted (agent commits are kept), emits one `file_diff` per changed file against the base commit, pushes, and reports `branch`, `commit_sha` and `pushed` on `done`; a failed push turns the session into `failed`. Failed, aborted and timed-out sessions push nothing, so a retry starts from the last good push. The proxy token reaches git only through environment configuration scoped to the clone URL, so it is never written to `.git/config`; the runner pushes to the configured URL rather than `origin` and skips hooks, because the agent controls `.git/config` and `.git/hooks`. `compare_ref` (the issue's base branch) is fetched as `origin/<compare_ref>` next to the single-branch clone, so a step continuing the agent branch can still diff against its base. A `read_only` workspace (reviewers, D42) is never committed or pushed; `done` reports the commit it was checked out at.

Tools (`allowed_tools` in `StartRequest`, from the role): harness-neutral names (`read`, `grep`, `glob`, `edit`, `write`, `bash`, `bash:readonly`, `web-fetch`); empty means the harness's defaults. The Claude Code adapter maps them to `--tools` (the other built-in tools are removed) and keeps `--dangerously-skip-permissions`, except with `bash:readonly`: then it runs in `--permission-mode dontAsk` with `--allowedTools` listing the tools and read-only command patterns (`git diff`, `git log`, `ls`, `cat`, ...), so any other shell command is denied without a prompt. Verified against Claude Code 2.1.323 by `TestSmokeRealClaudeReadOnlyTools`. The restricted shell narrows what a reviewer does; the boundary remains the read-only workspace and token.

The runner:

1. Clones the repo/branch (fresh clone, no host mounts), checks out or creates the step branch.
2. Writes harness-specific config from the role and provider profile.
3. Starts the harness and translates its protocol to the normalized `Event` stream.
4. On completion, commits, pushes to `agent/<issue-id>/*`, and reports the commit SHA.

## Harness adapters

| Harness | Headless interface | Notes |
|---|---|---|
| Claude Code | stream-JSON over stdin/stdout, or the Agent SDK | Expects an Anthropic-style API route (via the gateway). |
| opencode | `opencode serve` (HTTP API + SSE event stream) or native ACP (`opencode acp`) | Multi-provider. Implemented natively (D49). |
| pi | `pi --mode rpc` JSONL over stdin/stdout, with `steer` and `follow_up` commands | Multi-provider; supports mid-run messages. Implemented natively (D50). |

**ACP (Agent Client Protocol)** is a common JSON-RPC protocol for driving coding agents. opencode supports it natively; Claude Code and pi work through adapters (a Go adapter, `acp-adapter`, exists). The runner may prefer ACP where available and fall back to the native protocol. Verify adapter maturity before depending on it (open question).

Gateway (`gateway` in `StartRequest`, D49): the gateway's base URL, the run's virtual key and the API the gateway serves the step's model with (`anthropic` or `openai`, from the model's provider, D48). Each adapter turns it into its harness's configuration, so the control plane knows no harness's variables. The control plane still sets Claude Code's `ANTHROPIC_*` variables in `env` for runners that predate the field.

Adapter contract: map to the normalized event types - `assistant_message`, `tool_call`, `tool_result`, `file_diff`, `command_output`, `token_usage`, `error`, `done`.

Conformance (D51): every adapter runs one suite (`runnertest.RunConformance`) against its real binary and a scripted fake model that speaks both gateway APIs (`/v1/chat/completions` and `/v1/messages`, streamed or not), once per API the harness speaks, so it spends no model usage. A task through the gateway must call the shell tool, produce `assistant_message`, a `tool_call` and its successful `command_output`, and session-total `token_usage`, end with exactly one `done` (succeeded, exit code 0, the harness session ID, the last answer as result), report no `error`, and refuse further input; a message sent while the first model call is in flight must reach a later model call and the result; an abort mid-turn must end the session `aborted` with its reason before the tool runs; a read-only role must not be offered write tools and its shell must refuse a write. Every stream is also checked for payloads of the right type, `done` last, tool results answering announced calls, and token totals that never decrease. The binaries come from `mise.toml` (opencode, pi) or the official installer (Claude Code, in CI at the runner image's version); tests skip without them locally and fail in CI. On demand, `mise run test:smoke-opencode` and `test:smoke-pi` repeat the task and a steer that must change the running session against a real model, with OpenRouter standing in for the gateway (`runnertest.RunSmoke`; spends usage, needs `OPENROUTER_API_KEY`); both passed with `openai/gpt-5.6-luna` on 2026-11-14.

Claude Code adapter (`internal/runner/claudecode`): runs `claude -p --input-format stream-json --output-format stream-json --verbose --dangerously-skip-permissions` (the pod is the sandbox; the flag is refused as root, matching FR-SB-1). Text blocks become `assistant_message`, `tool_use` becomes `tool_call`, results of the `Bash` tool become `command_output` and other tool results `tool_result` (output capped at 256 KiB per event), and each `result` line becomes a `token_usage` event with session totals (Claude Code reports tokens and turns per answered message but cost for the whole session; the adapter sums the former). A message sent before Claude Code starts its turn is queued and gets its own `result`; one sent while a model call is in flight is delivered into the running turn at its next model call (as "The user sent a new message while you were working"), so one `result` can answer several messages. The runner therefore closes stdin when a `result` reports `queued_turn_count: 0` (or, for versions that do not report it, once every message has its own `result`); a message written after that result is still read before Claude Code exits. Verified against Claude Code 2.1.323 with `mise run test:smoke-claude` (spends real usage) and the conformance suite (D51). `file_diff` comes from the runner's own `git diff` at publish time, not from tool inputs.

opencode adapter (`internal/runner/opencode`, D49): each session starts a private `opencode serve` on a loopback port with a random password, configured entirely through `OPENCODE_CONFIG_CONTENT`: the gateway is the only provider (`@opencode/ai/providers/openai-compatible` or `anthropic-compatible` by the model's API, base URL `<gateway>/v1`, key from an environment variable), serving only the step's model; the role prompt is an instructions file outside the workspace (appended to opencode's own prompt); the role's tools are the `build` agent's permission rules, which opencode applies after every other rule. Every rule list starts with a catch-all, so nothing is left to `ask`: without a list everything is allowed except `question` and `websearch`; with one everything else is denied (which also removes the tools from what the model is offered) and `bash:readonly` allows the same read-only command patterns as for Claude Code (`runner.ReadOnlyCommands`). The adapter also disables project configuration (the repo could loosen the rules), the model catalog download, update checks and opencode's free built-in provider. It creates one opencode session (with a title, which spares a model call), sends the task and steering messages as prompts with delivery `steer`, which opencode hands to the running agent loop at its next model call, and follows `/api/event`: `session.text.ended` becomes `assistant_message`, `session.tool.called` `tool_call`, results of the `shell` tool `command_output` (an error when it failed or exited non-zero) and other tool results `tool_result`, `session.usage.updated` `token_usage` (session totals; reasoning tokens count as output; turns are model steps), and retries and failed steps `error`. The session ends when an execution ends with nothing left in the opencode session's inbox; a failed or interrupted execution fails it. The runner then stops the server's process group. A permission request no rule decided is refused (a safety net). Verified against opencode 2.2.7 by `internal/runner/opencode`'s tests, which run the real binary (pinned in `mise.toml`) against a fake OpenAI-compatible endpoint with the agent pod's proxy settings, spending no model usage. The runner image installs the same version from its GitHub release, checksum verified.

pi adapter (`internal/runner/pi`, D50): each session runs `pi --mode rpc --no-session` with its own agent directory outside the workspace (`PI_CODING_AGENT_DIR`) holding a `models.json` whose only custom provider is the gateway (`openai-completions` at `<gateway>/v1`, or `anthropic-messages` at the gateway's root with the key also sent as a bearer token), serving only the step's model, with the key read from an environment variable. Nothing that could add tools, change the prompt or reach other services is loaded from the repo or the home directory: extensions, skills, prompt templates, themes and MCP are off, project-local `.pi` files are ignored, and network activity beyond model calls is disabled (`--offline`, no version check, no telemetry); context files (`AGENTS.md`) still load. The role prompt is appended from a file (`--append-system-prompt`). The role's tools become `--tools` (`glob` is pi's `find` and `ls`; pi has no web fetch tool, so a role allowing `web-fetch` on pi is rejected when the definition is resolved). pi has no permission rules, so `bash:readonly` loads a small embedded extension (`readonly.ts`, the only extension loaded) whose `tool_call` handler blocks any `bash` call that is not a pipeline of `runner.ReadOnlyCommands`, or that contains redirections, command lists, substitutions or several lines. The task and steering messages are `prompt` commands with `streamingBehavior: "steer"`, which pi delivers after the current turn's tool calls, before the next model call. Each `message_end` of an assistant message gives `assistant_message` for its text, `tool_call` for its tool calls and a `token_usage` event with session totals (pi reports usage per message; turns count assistant messages); `tool_execution_end` gives `command_output` for `bash` and `tool_result` otherwise; retries, failed compaction and extension errors give `error`. When pi emits `agent_settled`, the adapter asks `get_state` and ends the session if nothing is streaming, compacting or pending: it fails if the run was aborted or the last assistant message was an error, and otherwise succeeds. It then closes stdin, which lets pi exit after disposing its work, and terminates it after a grace period if it does not. Verified against pi 1.5.2 by `internal/runner/pi`'s tests (real binary, fake OpenAI-compatible endpoint, the agent pod's proxy settings), which share their fake model, recorder and pod environment with opencode's in `internal/runner/runnertest`. The runner image installs the release to `/opt/pi` (the binary loads assets from its own directory), checksum verified.

## Models and providers

- **Provider profiles** hold credentials reference, base URL and limits. They are separate from **model refs** used per step (e.g. `anthropic/claude-sonnet-5-5`). Mixing models inside one workflow is expected.
- Harness/model compatibility is validated when a workflow is saved.

Implementation (D48): provider profiles are platform configuration, `llmGateway.providers` in the Helm values: per provider its LiteLLM provider name (`anthropic`, `openai`, `openrouter`), a Secret holding its key (`api-key`), the model ids it serves and optionally a base URL (`apiBase`) and per-model limits (`rpm`, `tpm`). The chart generates the gateway's model list from them and hands names and models to the control plane. A model ref is `<provider>/<model id>`; the gateway serves the model under its id alone (the name a virtual key is granted and the harness asks for), so ids are unique across providers. Each provider natively serves one API (`anthropic`: Anthropic Messages; `openai`, `openrouter`: OpenAI-compatible) and each harness speaks a set of them (Claude Code: Anthropic; opencode and pi: both), so the gateway never translates between APIs. The platform default model (`controlPlane.agent.model`) is a model ref too, and must be served.
- **LLM gateway (LiteLLM)** sits in front of every provider:
  - real API keys never enter a sandbox; each run receives a short-lived virtual key;
  - budgets per run and per issue are enforced in one place (D53: each session's key is capped at the lower of the step's budget and what is left of the issue's; LiteLLM refuses the key's requests once its recorded spend reaches the cap, so a request already in flight can overrun it slightly);
  - token and cost data is collected uniformly regardless of harness (D53: when a session ends, the control plane reads its key's spend, found by the key's alias once the gateway has written it, and adds it with the harness's token totals to the StepRun; runs and issues sum their steps, and the runs list and run page show them; models LiteLLM cannot price need prices in their provider profile, or their spend is zero and no budget applies);
  - enables fallbacks and routing later.

## Human intervention, least to most invasive

1. **Ask-human tool**: an MCP tool the agent calls when stuck. It blocks on a Temporal Signal answered from the UI.
2. **Steer**: inject a message into a running session via the runner (`Steer`). Harnesses without native support get it at the next turn boundary.
3. **Abort** a step via Signal. **Pause** is deferred (D52): no harness pauses natively, and a steer ("stop and wait", "change direction") or an abort followed by re-running the issue covers its uses.

Implementation (D52): the run page shows the running agent step (`GET /api/runs/{id}/steps`) with a message box; **Steer** posts `POST /api/steps/{id}/steer` (`{"text", "by"}`) and **Abort** `POST /api/steps/{id}/abort` (`{"reason", "by"}`). The API accepts them only for a running agent step and sends the run's workflow the signal `step-steer` or `step-abort`, naming the StepRun; without authentication (open question 7) the sender is self-reported, as for gate decisions. While an agent session runs, `Interpret` relays each signal for that step to its runner through the `InterveneSession` Activity (runner `Steer`, or `Abort` with "aborted by <who>: <reason>") and records an `intervention` event (action, who, text, and whether the session still took it) in the transcript; signals for other steps are ignored. Every harness delivers a steer into the running turn at its next model call (Claude Code, opencode, pi: verified by the conformance suite, D51). An abort ends the session `aborted`, pushes nothing, and fails the run with "aborted by <who>: <reason>". Behind `workflow.GetVersion("step-interventions")`.
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
