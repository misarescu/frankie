# CLAUDE.md

Guidance for coding agents (and humans) working in this repository. `AGENTS.md` is a copy of this file.

## What this repo is

The Software Factory platform. The specs in `docs/` are the source of truth until code exists; when code and spec disagree, fix one of them in the same PR. Read `docs/01-architecture.md` and the roadmap milestone you are working on before changing anything.

## Stack and conventions

- Go (latest stable pinned in `mise.toml`), standard layout, `internal/` for everything not meant to be imported. Errors wrapped with `%w`. `context.Context` first parameter. No global state. Table-driven tests.
- Postgres with versioned SQL migrations (golang-migrate or goose; decide once in M0 and record in `docs/00`). Queries via sqlc. No ORM.
- Temporal Go SDK for durable workflows.
- gRPC + protobuf (`buf`) for runner and driver interfaces. Generated code is committed.
- UI: React + Bun in `web/`. TypeScript strict.
- Deployment: Helm chart in `deploy/helm/`, Flux manifests in `deploy/flux/`.
- Tooling is declared in `mise.toml`. Everything is run through `mise run <task>`: `build`, `test`, `lint`, `generate`, `e2e`. If a task is missing, add it rather than inventing a command.

## Invariants (never violate; changes need explicit human approval)

1. **Agents never receive `kubectl`, cluster credentials, host mounts, or real model API keys.** Sandboxes get a short-lived LiteLLM virtual key and a git token scoped to push to `agent/*` branches only.
2. **Never trust agent-reported results.** Every `command` gate re-runs from the pushed commit in a fresh pod.
3. **Workflow code is deterministic.** Anything non-deterministic (time, randomness, I/O, map iteration order) goes in an Activity. Any change to workflow code needs Temporal versioning (`workflow.GetVersion`) and passing replay tests.
4. **The workflow definition is snapshotted into the workflow input at start.** Never re-read YAML mid-run.
5. **The factory never deploys over the instance doing the work.** See `docs/08-self-hosting.md`.
6. **Protected paths require a human gate**: NetworkPolicies, RBAC, Kata runtime class, runner, gate definitions, LiteLLM budgets, lint/CI config, toolchain files, and the protected-paths list itself. Skipped or deleted tests also trigger the gate.
7. **Control plane core stays provider-neutral.** GitHub specifics live behind `GitProvider` / `IssueSource`. Flux specifics live behind `ReleaseDriver`. No provider types leak into `internal/domain`.
8. **Postgres owns all state except in-flight workflow state, which Temporal owns.** Postgres rows reference Temporal workflow IDs; never duplicate workflow state.

## Working agreements

- Small PRs, one issue each. Branch name `agent/<issue-id>/<slug>` when an agent opens it.
- Every behavior change has a test. Tests for a feature are derived from the spec's acceptance criteria, not from the implementation.
- New workflow behavior gets a replay test fixture.
- New config fields are validated at save time (schema + cross-checks such as harness/model compatibility and driver capabilities).
- Write ADR-style entries in `docs/00-vision-and-decisions.md` for any decision that changes an interface.
- Do not add dependencies casually; prefer the standard library. New dependencies need a one-line justification in the PR.
- No emojis in code, docs or commit messages.

## Where things live

See `docs/01-architecture.md` for the repo layout. Short version: `cmd/` binaries, `internal/` packages, `proto/` contracts, `web/` UI, `deploy/` Helm and Flux, `roles/` and `workflows/` example config, `docs/` specs.

## Definition of done for a task

Builds, `mise run lint` and `mise run test` pass, new behavior has tests tagged with the issue ID, docs updated if an interface or invariant changed, no protected paths touched without calling it out in the PR description.
