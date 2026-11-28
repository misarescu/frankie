# 07 - Observability and control

Three layers, plus a ladder of human intervention.

## 1. Live session view (per agent run)

- The runner normalizes harness events into one stream: assistant messages, tool calls, file diffs, command output, token usage.
- Full profile: published to NATS or Redis Streams and pushed to the UI over SSE/WebSocket. Concrete profile: Postgres `LISTEN/NOTIFY` or in-process fan-out.
- Streams are **persisted** (Postgres plus object storage/PVC) so any run can be replayed as a transcript.
- **FR-OB-1** A user can open a running step and see the live stream with sub-second latency from the pod.
- **FR-OB-2** A completed run can be replayed from persisted events with no dependency on the pod.
- M1 implementation (`internal/api`): the store calls `pg_notify('frankie_events', <run id>)` with every event (on commit inside a transaction); a hub with one `LISTEN` connection wakes the streams of that run. `GET /api/runs/{id}/stream` is server-sent events: stored events, then live ones, resumable with `Last-Event-ID`, ending with an `end` event once the run is finished. `GET /api/runs/{id}/events?after=` replays as JSON. The control plane serves only this API (on its API port, not reachable from agent pods); the UI is the separate `web/` deployment (React + Bun), which proxies `/api` to the control plane so the browser has one origin. Until auth is decided (open question 7), reach it with `kubectl -n frankie port-forward svc/frankie-web 3000`.
- M5.1 (D67): the run page labels events in plain words ("Agent", "Tool: Bash", "Status: Step pending → running", a gate's result per command or reviewer) and groups consecutive events under a heading naming their step (and attempt) and who did the work. Events carry `step_run_id`; `GET /api/runs/{id}/steps` adds each step's `role`, `harness` and `model` (agent steps; no model means the platform's default) or `gate` type, read from the run's snapshot. The issue page lists runs by short ID, date and workflow (`refine@1` for a refinement).

## 2. Workflow view (per issue and per release)

A timeline of issue > steps > runs > gates, with loop counts and gate results, built on the platform's own domain model. The Temporal UI remains available for debugging but is not the product UI.

- M5.1 (D68): the issue page has a live view. The issue detail lists `live_runs`, the pending and running runs of the issue and of every issue under it (so an epic shows its tasks' runs), oldest first. For each, the page follows the run's stream and shows its step timeline (status, duration, who) and the agent's latest messages, tool calls, questions, interventions and gate results; a run that finishes while the page is open stays with its outcome. Since D73 the card also lists the steps not reached yet (`GET /api/runs/{id}/plan`), greyed, and each agent step's cost, and an issue that does the work always shows its latest run. Since D98 a conditional step the run went past shows as skipped, with its condition, where it would have run. The page polls every 3 s while something runs or right after a start, otherwise every 10 s.

## 3. Metrics and traces

OpenTelemetry: one trace per workflow, a span per step, child spans per LLM and tool call; exported to Grafana Tempo/Prometheus/Loki, or Langfuse for LLM-specific traces.

Key metrics (these drive model/harness choice per step):

- cost, tokens and wall time per issue and per step
- **loop count per issue** (high = weak spec)
- pass/fail rate per gate
- **first-pass review rate per model/harness combination**
- merge queue wait time, rebase conflict rate
- release verification failure rate, rollback count

Concrete profile: Temporal UI, the factory's own run view and `kubectl logs`; add a small VictoriaMetrics later for metrics.

## Human intervention ladder

1. **Ask-human tool** (agent-initiated).
2. **Steer** a running session.
3. **Pause/abort** a step.
4. **Take over** via web terminal into the pod.

All interventions are recorded as events with the acting user.

## UI surfaces (from the product brief: "nice management")

M9 (D141, D142) rebuilds every surface below in one shell: a sidebar with the project switcher, Board, Runs, Approvals and Releases, breadcrumbs from the project down to a run's step, a search palette, and path URLs (`/projects/{project}/board`, `/issues/{key}`, `/runs/{id}?step=`, `/releases/{id}`, `/approvals`) in place of the hash routes named below. A parent issue shows the cost of everything under it. The M5-M7 entries below describe what each surface shows.

- Project board / issue list with DAG view and status; start one/many.
  - M5 API (D59): `GET /api/projects`, `GET /api/issues?project=<name>` (the whole project with parents, dependency keys, cost and latest run) and `GET /api/issues/{key}` (the issue page: spec, relations, runs, usage, change requests). Both carry `total_cost_usd`, the issue's own cost plus that of every issue under it (D142).
  - M5 UI (`#/issues`): the project's issues as their hierarchy with status, what each waits for, cost and latest run; finished issues hidden unless asked for; the dependency graph drawn in layers (an issue to the right of what it waits for); selected issues start together and the scheduler's plan (starting, waiting with why, overlaps) is shown.
- Issue detail: spec editor, acceptance criteria, run history, live view, cost. M5 (`#/issues/<key>`): spec and acceptance criteria, a refined spec waiting for approval with approve and reject, relations, runs and cost, change requests, file hints, changed paths and overlaps, and start and retry; editing the spec in the page is not built yet.
- Approvals inbox: pending human gates (spec approval, protected-path changes, prod promotion, flag rollout steps). M5 (D64, D66): agents' questions, then human gates, then refined specs waiting for approval with the refiner's summary, the original text and a link to the refinement's transcript (`GET /api/specs`, decided with `POST /api/issues/{key}/spec`).
- Merge queue view per repo.
- Release view: stage progression, verification results, rollback, linked auto-bugs.
  - M7 API (D130): `GET /api/releases?project=<name>` (each release's version, status, issues and stages) and `GET /api/releases/{id}` (notes, the provider release, the bug issue, issues, deployments with their verification, the release agent's runs and cost). Each deployment records how its verification ended; `GET /api/runs` lists release runs too.
  - M7.1 API (D131): `GET /api/version` answers `{"version": "<commit>"}`, the commit the control plane was built from (`unknown` for a build that stamped none), which a preview's driver reads to know the preview runs its head.
  - M7 UI (`#/releases`, `#/releases/<id>`): the project's releases with version, status, each stage's outcome and issues; a release's page shows its version or why it has none, the provider release, each stage's deployment with its reason, its verification and the failed tests with the issues they are tagged with, the bug issue, the notes and who wrote them, the issues, and the release agent's runs, whose transcripts open like any run's. The run list names release runs by their release.
- Workflow and role editors with save-time validation feedback.
- Settings: connectors, provider profiles, budgets, protected paths, deployment profile.
