# 01 - Architecture

## Component overview

```
                  +-----------------------------------------------+
   Browser  ----> |  Web UI (React)  <->  Control plane API (Go)  |
                  +----------------------+------------------------+
                                         |
        +--------------------------------+---------------------------------+
        |                                |                                 |
   Postgres                        Temporal                          LLM gateway
 (domain state,                (durable workflows:                    (LiteLLM:
  events, metrics)              interpreter, merge queue)           virtual keys, budgets)
                                         |                                 ^
                              Activities launch pods                        |
                                         v                                 |
                  +-----------------------------------------------+        |
                  |  Agent pod (Kata runtime class, per step)      |--------+
                  |   runner (Go) <-gRPC-> harness (claude-code,   |
                  |   opencode, pi)   + toolchain from mise        |
                  +----------------------+------------------------+
                                         | git push agent/* only
                                         v
                                   Git provider (GitHub)

   Release drivers (out-of-process plugins) <- executed only by control plane Activities
     flux, flux-preview, flagd, (later: argocd, github-releases, registries)
```

## Components

### Control plane (Go)
Single deployable with these internal modules:

- **api**: HTTP/JSON (and SSE/WebSocket for live streams) for the UI and CLI. Auth: single-user/OIDC to start (open question).
- **domain/store**: entities and Postgres repositories (see `02-domain-model.md`).
- **scheduler**: decides which ready issues start, honoring the DAG, concurrency limits, budgets and the "likely file overlap" warning.
- **workflow**: the Temporal workflow interpreter and Activities (see `03-workflows-and-gates.md`).
- **mergequeue**: one long-running Temporal workflow per repo (see `06-delivery.md`).
- **connectors**: `GitProvider`, `IssueSource` implementations (GitHub first).
- **drivers host**: loads `ReleaseDriver` plugins over gRPC (hashicorp/go-plugin).
- **events**: normalized event stream ingestion, persistence and fan-out.
- **images**: toolchain image hashing/building/registry lookup.
- **gitproxy**: git smart-HTTP proxy for agent pods (D21). Validates the per-step token, allows reads of the bound repo and ref updates only under `refs/heads/agent/<issue-key>/`, and forwards with the `GitProvider` credential.

### Runner (Go, runs inside every agent pod)
Small binary next to the harness. The control plane talks to it over one gRPC interface (`Start`, `Prompt`, `Steer`, `Abort`, `Events`). Details in `04-agents-harnesses-models.md`.

### Egress proxy (Go, separate deployment)
CONNECT-only forward proxy for agent pods with a hostname allowlist (D20). The only route from an agent pod to the internet; it never dials loopback, link-local or private addresses.

### Temporal
Runs the generic interpreter workflow and the per-repo merge queue workflow. Human gates are Signals with timeouts. Temporal's UI is kept for debugging.

### Postgres
Everything outside Temporal: issues, projects, workflow definitions (versioned), runs, step runs, gate results, change requests, merge queue entries, releases, deployments, flags, events, metrics. One instance serves the app, Temporal and LiteLLM (separate databases/schemas).

### LiteLLM gateway
All model traffic goes through it. Real API keys exist only here. Each run gets a short-lived virtual key with budget limits. Collects token/cost data regardless of harness.

### Delivery
FluxCD reconciles environments from a GitOps repo. Preview environments via Flux Operator ResourceSets keyed on change requests. Agents never touch the cluster.

## Boundaries (important for review)

| Boundary | Rule |
|---|---|
| Control plane <-> agent pod | gRPC to the runner; the pod reaches back only to the git proxy. Pod egress goes through the egress proxy (D20). |
| Agent pod <-> git | Clone + push through the control plane git proxy with a short-lived per-step token, limited to `agent/<issue-key>/*` (D21). Git history is the workspace handoff between steps. |
| Core <-> provider | Neutral interfaces; no GitHub/Flux types in `internal/domain`. |
| Workflow <-> world | All I/O in Activities. Workflow code is deterministic. |
| Agent <-> deployment | None directly. Optional MCP server exposes scoped driver tools: reversible/harmless calls (e.g. `Status`, publish to beta) allowed; irreversible ones (publish to stable) block on a human gate. |

## Repository layout (target)

```
cmd/
  controlplane/     main for API + workers
  runner/           in-pod runner
  factoryctl/       CLI (start issues, approve gates, tail runs)
  egress-proxy/     hostname-allowlisting CONNECT proxy for agent pods
internal/
  api/
  domain/           entities, state machines (no provider types)
  store/            schema.sql (desired state), migrations/ (goose format,
                    generated by Atlas), queries/ (sqlc input), db/ (sqlc output)
  scheduler/
  workflow/         interpreter workflow, activities, replay fixtures
  mergequeue/
  connectors/
    github/
  drivers/          driver host + built-in drivers (flux, flux-preview, flagd)
  gateway/          LiteLLM admin client (virtual keys, budgets)
  events/
  images/
  gitproxy/         git smart-HTTP proxy for agent pods
  egress/           egress proxy policy and server
  config/           YAML schemas, validation
  runner/           in-pod runner: RunnerService server, event log, harness adapters (claudecode/)
proto/              runner.proto, driver.proto
gen/                generated protobuf/gRPC Go code (committed; public so external drivers can import it)
web/                React + Bun UI
deploy/
  helm/factory/     the platform chart; values.yaml + values-<profile>.yaml
  flux/clusters/    Flux manifests per cluster (bootstrap path)
  kata/             kata-deploy values for the cluster runtime (protected)
roles/              example role definitions
workflows/          example workflow definitions
scripts/            helpers called by mise tasks
docs/
mise.toml, mise.lock
atlas.hcl, buf.yaml, buf.gen.yaml, sqlc.yaml, .golangci.yml
```

## Cross-cutting concerns

- **Config validation at save time**: workflow definitions are validated for schema, step references, loop bounds, harness/model compatibility, and driver capabilities (e.g. a stage requiring rollback on a driver that cannot roll back is rejected).
- **Versioning**: roles, workflows and platform base image are versioned. A run records the exact versions used.
- **Idempotency**: every Activity is idempotent or carries an idempotency key; pods are labeled with run/step IDs so retries reuse or clean up.
- **Secrets**: Kubernetes Secrets managed through the GitOps repo (SOPS or external-secrets; open question). Never in Postgres plaintext.
