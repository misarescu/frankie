# AGENTS.md

Guidance for coding agents (and humans) working in this repository. `CLAUDE.md` imports this file; edit this one.

## What this repo is

The Software Factory platform. The specs in `docs/spec/` are the source of truth; when code and spec disagree, fix one of them in the same PR. `docs/notes/` holds working notes (e.g. M0 exploration). Read `docs/spec/01-architecture.md` and the roadmap milestone you are working on before changing anything. `README.md` has a map of the spec files.

**Status: M3 complete** (`docs/spec/10-roadmap.md`): every issue runs the generic `Interpret` workflow on a definition snapshotted at start (D39, D41): the built-in `default-build`, or the repo's own from `.factory/workflows/`, with roles from `roles/` or `.factory/roles/` (D40). Agent steps run in the repo's toolchain image, pinned by digest (D45). `command` gates re-run `mise run lint` and `mise run test` from the pushed commit in a fresh pod; parallel groups and `agent-verdict` gates run read-only reviewers (D42); failed gates loop back with feedback up to `maxLoops`, then escalate. `human` gates wait for a decision in the web UI's approvals inbox (D43), and changes to protected paths or tests force one after the change request opens (D44). Frankie itself is onboarded as project `frk` (D46). Next: M4 (harnesses and models). Still manual: merging change requests (M6), re-running a failed issue (a `failed` to `ready` transition in Postgres), and `cmd/factoryctl`; the API has no authentication (open question 7). The layout in `docs/spec/01-architecture.md` is the target.

## Commands

Everything runs through mise tasks; tool versions are pinned in `mise.toml` (locked in `mise.lock`). If a task is missing, add it to `mise.toml` rather than inventing a command.

- `mise install` then `mise run doctor`: install pinned tools, then check tools, Docker and the cluster.
- Watch runs: `kubectl -n frankie port-forward svc/frankie-web 3000`, then open http://localhost:3000 (run list and live transcripts). Develop the UI with `mise run web:dev` (proxies `/api` to `CONTROLPLANE_URL`, default http://localhost:8080). The UI is React + Bun in `web/`; `mise run lint` and `mise run test` include its type check and `bun test`.
- `mise run build | test | lint | generate | e2e`. CI runs `generate` and fails if it changes anything, then `lint`, `build`, `test`.
- Single Go test: `mise exec -- go test ./internal/<pkg> -run '<TestName>'`. Extra args to `mise run test` are appended to `go test ./...`.
- Cluster acceptance tests live in `e2e/`: Ginkgo v2 + Gomega (D25), build tag `e2e`, environment variables in `e2e/e2e_suite_test.go`. Run them with `FRANKIE_E2E_RUNNER_IMAGE=ghcr.io/misarescu/frankie-runner:sha-<commit> FRANKIE_E2E_REPO=misarescu/Goodreads-Clone mise run e2e`; narrow with `mise exec -- ginkgo --tags=e2e --label-filter=sandbox ./e2e` (labels: `sandbox`, `platform`, `gateway`, `gitproxy`, `services`, `gomodules`, `toolchain`; `toolchain` builds an image and takes about 12 minutes on the concrete profile). Unit tests stay on the standard `testing` package.
- Platform images (`deploy/images/`) are built by CI and published to GHCR on merges to `main` (D23). Claude Code crashes under QEMU, so build the runner image locally only for your native platform.
- To verify an unpublished Go image (not the runner) on the homelab cluster before merge: `docker buildx build --platform linux/amd64 -f deploy/images/<name>/Dockerfile -t docker.io/library/frankie-<name>:dev --load .`, then `docker save docker.io/library/frankie-<name>:dev | ssh homelab@192.168.3.7 sudo k3s ctr images import -`, then `helm upgrade` with `--set images.overrides.<name>=docker.io/library/frankie-<name>:dev`. Ask before changing the cluster.
- `mise run test:race` runs the suite under the race detector (also in CI). `mise run test:smoke-claude` checks the Claude Code adapter against the real `claude` CLI; it spends model usage, so run it only when changing the adapter or upgrading Claude Code.
- Store tests need Postgres: `mise run db:up` (Docker, port 55432; `db:down` removes it). Each test creates and drops its own database. Without Postgres they skip locally but fail in CI. `mise.toml` sets `FRANKIE_TEST_DATABASE_URL` and overrides the shell environment inside `mise run`/`mise exec`.
- Schema changes: edit `internal/store/schema.sql`, then `mise run db:diff <name>` (needs Docker) to generate a goose migration. Never hand-edit generated migrations without `mise run db:hash`.
- Migrations must stay safe for the previous control plane binary (expand, then contract): add columns and relax constraints in one release, drop or tighten them in a later one. The chart deploys the control plane with `Recreate`, but that is a safety net, not a licence to break the previous version.
- `generate` and proto lint skip themselves until `proto/` or `internal/store/queries/` have files.

## How it fits together

1. An issue starts: the control plane starts the generic Temporal `Interpret` workflow, with the project's YAML workflow definition snapshotted into the workflow input.
2. Activities launch one Kata pod per agent step. The in-pod runner talks gRPC to the control plane (`Start`, `Prompt`, `Steer`, `Abort`, `Events`) and wraps the harness (Claude Code, opencode, pi). Model traffic goes through LiteLLM with a per-run virtual key.
3. The agent pushes to `agent/*`. Git history is the workspace handoff between steps.
4. Gates: `command` gates re-run in a fresh pod from the pushed SHA; `agent-verdict` and `human` gates follow. `onFail` can loop back to an earlier step, bounded by `maxLoops`.
5. A per-repo merge-queue workflow (signal-with-start) rebases, re-gates and merges.
6. A Release (a SHA plus the issues it contains) moves through project-defined Stages via out-of-process `ReleaseDriver` gRPC plugins, executed only by control-plane Activities.

Details: workflows and gates `docs/spec/03`, runner and harnesses `docs/spec/04`, sandbox `docs/spec/05`, merge queue and releases `docs/spec/06`.

Use the spec's vocabulary in code and docs (defined under Terminology in `docs/spec/00-vision-and-decisions.md`): Issue, Run, Step, Role, Harness, Gate, ChangeRequest (never "PR" in core code), Release, Stage, Driver.

## Stack and conventions

- Go (latest stable pinned in `mise.toml`), standard layout, `internal/` for everything not meant to be imported. Errors wrapped with `%w`. `context.Context` first parameter. No global state. Table-driven tests.
- Postgres. Migrations: Atlas authors, goose applies (D19). Queries via sqlc with pgx/v5. No ORM.
- Temporal Go SDK for durable workflows.
- gRPC + protobuf (`buf`) for runner and driver interfaces. Generated code is committed to `gen/`.
- `.golangci.yml` enforces invariant 7 with a depguard rule on `internal/domain`.
- UI: React + Bun in `web/`. TypeScript strict.
- Deployment: Helm chart in `deploy/helm/`, Flux manifests in `deploy/flux/`.
- Tooling is declared in `mise.toml`.

## Concrete profile

Every milestone must run on the **concrete profile**: a 16 GiB laptop running single-node k3s with reduced services (see `docs/spec/09-deployment-profiles.md`). Profile differences are configuration, not forks (D18). Check memory budgets and the profile's deferrals before adding a service or a pod.

## Invariants (never violate; changes need explicit human approval)

1. **Agents never receive `kubectl`, cluster credentials, host mounts, or real model API keys.** Sandboxes get a short-lived LiteLLM virtual key and a git token scoped to push to `agent/*` branches only.
2. **Never trust agent-reported results.** Every `command` gate re-runs from the pushed commit in a fresh pod.
3. **Workflow code is deterministic.** Anything non-deterministic (time, randomness, I/O, map iteration order) goes in an Activity. Any change to workflow code needs Temporal versioning (`workflow.GetVersion`) and passing replay tests.
4. **The workflow definition is snapshotted into the workflow input at start.** Never re-read YAML mid-run.
5. **The factory never deploys over the instance doing the work.** See `docs/spec/08-self-hosting.md`.
6. **Protected paths require a human gate**: NetworkPolicies, RBAC, Kata runtime class, runner, gate definitions, LiteLLM budgets, lint/CI config, toolchain files, and the protected-paths list itself. Skipped or deleted tests also trigger the gate.
7. **Control plane core stays provider-neutral.** GitHub specifics live behind `GitProvider` / `IssueSource`. Flux specifics live behind `ReleaseDriver`. No provider types leak into `internal/domain`.
8. **Postgres owns all state except in-flight workflow state, which Temporal owns.** Postgres rows reference Temporal workflow IDs; never duplicate workflow state.

## Decisions and open questions

- Decisions live in the numbered log (D1, D2, ...) in `docs/spec/00-vision-and-decisions.md`. Add new ones at the bottom; never rewrite an entry, supersede it with a new one.
- Before making a choice, check `docs/spec/11-open-questions.md` (e.g. migration tool, secrets management, auth, transcript storage). When you resolve one, record it as a decision in `docs/spec/00` and update `docs/spec/11`.
- Write a decision entry for anything that changes an interface.

## Working agreements

- Small PRs, one issue each. Branch name `agent/<issue-key>/<slug>` (e.g. `agent/frk-42/fix-login`, D22) when an agent opens it.
- Every behavior change has a test. Tests for a feature are derived from the spec's acceptance criteria, not from the implementation.
- One test file per implementation file: `policy.go` is tested in `policy_test.go`, not in a combined package test file.
- New workflow behavior gets a replay test fixture.
- New config fields are validated at save time (schema + cross-checks such as harness/model compatibility and driver capabilities).
- Do not add dependencies casually; prefer the standard library. New dependencies need a one-line justification in the PR.
- No emojis in code, docs or commit messages.

## Where things live

See `docs/spec/01-architecture.md` for the repo layout. Short version: `cmd/` binaries, `internal/` packages, `proto/` contracts, `gen/` generated code, `web/` UI, `deploy/` Helm and Flux, `roles/` and `workflows/` example config, `scripts/` task helpers, `docs/spec/` specs, `docs/notes/` notes.

## Definition of done for a task

Builds, `mise run lint` and `mise run test` pass, new behavior has tests tagged with the issue ID, `docs/spec/` updated if an interface or invariant changed, no protected paths touched without calling it out in the PR description.
