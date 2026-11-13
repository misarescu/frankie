# 00 - Vision and decisions

## Goal

Turn a homelab into a software factory: engineers write specs, role-based agents implement, verify and review them in sandboxes, merges are serialized and automatic once gates pass, and releases move through project-defined stages to production with feature-flag rollout. Strong management UI, workflows, customization and deployment options.

The key distinction: an agent that can write code is not a system that reliably produces software. The value of this project is the control plane, the gates and the delivery pipeline, not the agent loop.

## Non-goals

- Writing our own agent loop or model client.
- Sprints, story points or other human ceremony. Internally a "batch" is a set of ready issues with a DAG.
- Release branches, cherry-picks and back-merges.
- Giving agents any direct cluster or production access.
- Supporting non-Kubernetes execution at first (external executors are a later extension).

## Decision log

Numbered for reference from PRs and issues. Add new decisions at the bottom; never rewrite history, supersede instead.

| # | Decision | Rationale |
|---|---|---|
| D1 | No sprints. Work is a list of issues (specs), optionally grouped as epic > story > task, with a dependency DAG. Engineer sets order or lets independent issues run in parallel, and starts one or many. | Sprints are a human ceremony. |
| D2 | Lifecycle: build (per issue) > merge queue (per repo) > release stages (per release), configured per project. | Separates concerns that scale differently. |
| D3 | Connectors for git source, issue source and deployment target (`GitProvider`, `IssueSource`, `ReleaseDriver`). | Provider neutrality. |
| D4 | Control plane separate from the agent loop; the loop is an editable workflow definition. | Swap or upgrade agents freely. |
| D5 | Harnesses: Claude Code, opencode, pi first, each with its own provider config. Models can be mixed per workflow step. | Avoid lock-in; use cheap models where adequate. |
| D6 | Gates are one concept with three types: `command`, `agent-verdict`, `human`. | Uniform handling of retries and escalation. |
| D7 | Runtime: Kubernetes with Kata Containers for agent pods; Temporal for durable workflows. | Isolation for unattended runs; durability. |
| D8 | Delivery via pluggable release drivers. GitOps with FluxCD is the first driver. Argo CD, GitHub Releases, registries etc. follow behind the same interface. | Not every project deploys to Kubernetes. |
| D9 | A dedicated e2e test agent writes tests from the spec, in parallel with the coder. Its tests merge with the issue. | Tests that check the spec rather than mirror the code. |
| D10 | Auto-merge once all gates pass, serialized by a control-plane-owned merge queue. | Independent of git provider tiers (GitLab merge trains are paid-only). |
| D11 | Feature flags, not release branches. Per-project optional. OpenFeature SDKs with flagd as the first provider. | `main` stays deployable; agents are bad at multi-branch git. |
| D12 | GitHub first. Neutral `ChangeRequest` entity; UI labels it PR or MR per provider. | |
| D13 | Postgres for all state outside Temporal. | One database, simple. |
| D14 | Preview environment per change request from the start, via Flux Operator ResourceSets, so e2e tests can gate merges. Flux stays (no Argo switch). | Supersedes the earlier idea that e2e could only run on staging. |
| D15 | Toolchains: platform base image + per-repo `mise.toml`, built into images by the factory with rootless BuildKit, pushed to an in-cluster registry. Escape hatch: `.factory/Dockerfile` with `FROM factory-base:vX`. | Humans, CI and agents share one version pin file. |
| D16 | Agent steps all run in Kubernetes pods for now. Optional `runsOn:` for external executors later (e.g. macOS signing/notarization). | |
| D17 | Self-hosting is a goal: the factory is a project on its own board, with extra guardrails. See `08-self-hosting.md`. | Best dogfooding signal. |
| D18 | Two deployment profiles: **full** and **laptop** (single-node k3s, reduced services). Profile differences are configuration, not forks. See `09-deployment-profiles.md`. | The first host is a 16 GiB laptop. |
| D19 | Migrations: Atlas authors, goose applies. `internal/store/schema.sql` is the desired state; `atlas migrate diff` (env `local` in `atlas.hcl`) writes goose-format files to `internal/store/migrations`; the control plane embeds them and applies them with goose as a Go library. sqlc reads the same migration files. Only Atlas's free features are relied on (diffing, versioned migrations); Pro features (migration lint, drift detection) are not. Resolves open question 9. | Declarative schema plus generated diffs, without a proprietary-licensed binary at runtime. Atlas needs a throwaway dev database to diff (Docker locally, a Postgres sidecar in agent pods). |
| D20 | Agent egress goes through the factory's own **egress proxy** (`cmd/egress-proxy`): a CONNECT-only forward proxy that allows listed hostnames and refuses to dial loopback, link-local and private addresses. NetworkPolicy lets agent pods reach only DNS, the LLM gateway, the control plane and the proxy; tools use `HTTPS_PROXY`, and anything that bypasses it is dropped. Refines FR-SB-2. | Standard NetworkPolicy (what k3s enforces) matches IPs, not hostnames. A small proxy keeps the laptop budget; Cilium FQDN policies would cost ~300-500 MiB and a CNI reinstall. |
| D21 | Agent pods never hold a git provider credential. They clone and push through a **git proxy in the control plane** with a short-lived per-step token; the proxy accepts ref updates only under `refs/heads/agent/<issue-key>/` and forwards with the real credential. `GitProvider` supplies that upstream credential. Refines FR-SB-3. | GitHub tokens cannot be scoped to branches. The proxy scopes each step to its own issue's branches and works for any provider. |
| D22 | Issues get a short per-project key: `<project key prefix>-<number>`, e.g. `frk-42`. Prefixes are 2-10 lowercase letters or digits, start with a letter, and never change; numbers are allocated per project. Agent branches are `agent/<issue-key>/<slug>` and tests are tagged with the issue key. Resolves open question 13. | Readable in branches and test tags, and independent of the issue source. |
| D23 | Platform images (runner, egress proxy, later control plane) are built by CI on every merge to `main` and published to GHCR as `ghcr.io/misarescu/frankie-<name>:sha-<commit>` for linux/amd64. Clusters pull them directly. Toolchain images built inside the cluster (D15) still go to the in-cluster registry. | No node configuration or push access to the homelab from CI; builds are reproducible from a commit. |
| D24 | The **laptop** profile of D18 is renamed **concrete** (values file `values-concrete.yaml`, Helm value `profile: concrete`, Flux path `deploy/flux/clusters/concrete/`). It is still sized for one 16 GiB, 4-core laptop. Supersedes the profile name in D18; the profile itself is unchanged. | The profile describes a deployment, not a device; the name should not tie the project to laptops. |
| D25 | Cluster acceptance tests (`e2e/`, build tag `e2e`) use **Ginkgo v2 and Gomega**, run with the pinned `ginkgo` CLI (`mise run e2e`). Unit and package tests stay on the standard `testing` package, table-driven. | Readable specs and reports for multi-step cluster scenarios (`Ordered` containers, `Eventually`, labels, focus); unit tests gain nothing from a framework. |

## Terminology

- **Issue**: a spec. Unit of work for the build lifecycle.
- **Run**: one execution of a workflow for an issue.
- **Step**: one node in a workflow (agent, gate, parallel group, action).
- **Role**: system prompt + skills + tools + MCP servers + model + output contract.
- **Harness**: the agent CLI/SDK (Claude Code, opencode, pi).
- **Gate**: a pass/fail checkpoint of type `command`, `agent-verdict` or `human`.
- **ChangeRequest**: neutral name for PR/MR.
- **Release**: a git SHA plus the set of issues it contains.
- **Stage**: an ordered step in a project's release path (preview, staging, prod, rollout...).
- **Driver**: plugin that knows how to publish, promote, check and roll back for a stage.
