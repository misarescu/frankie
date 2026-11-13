# 07 - Observability and control

Three layers, plus a ladder of human intervention.

## 1. Live session view (per agent run)

- The runner normalizes harness events into one stream: assistant messages, tool calls, file diffs, command output, token usage.
- Full profile: published to NATS or Redis Streams and pushed to the UI over SSE/WebSocket. Concrete profile: Postgres `LISTEN/NOTIFY` or in-process fan-out.
- Streams are **persisted** (Postgres plus object storage/PVC) so any run can be replayed as a transcript.
- **FR-OB-1** A user can open a running step and see the live stream with sub-second latency from the pod.
- **FR-OB-2** A completed run can be replayed from persisted events with no dependency on the pod.

## 2. Workflow view (per issue and per release)

A timeline of issue > steps > runs > gates, with loop counts and gate results, built on the platform's own domain model. The Temporal UI remains available for debugging but is not the product UI.

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

- Project board / issue list with DAG view and status; start one/many.
- Issue detail: spec editor, acceptance criteria, run history, live view, cost.
- Approvals inbox: pending human gates (spec approval, protected-path changes, prod promotion, flag rollout steps).
- Merge queue view per repo.
- Release view: stage progression, verification results, rollback, linked auto-bugs.
- Workflow and role editors with save-time validation feedback.
- Settings: connectors, provider profiles, budgets, protected paths, deployment profile.
