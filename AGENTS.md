# AGENTS.md

Guidance for coding agents (and humans) working in this repository. `CLAUDE.md` imports this file; edit this one.

## What this repo is

The Software Factory platform. The specs in `docs/` are the source of truth until code exists; when code and spec disagree, fix one of them in the same PR. Read `docs/01-architecture.md` and the roadmap milestone you are working on before changing anything. `README.md` has a map of what each doc covers.

**Status: specification only.** Nothing in `cmd/`, `internal/`, `proto/`, `web/`, `deploy/` or `mise.toml` exists yet; the layout in `docs/01-architecture.md` is the target. The first work is M0 in `docs/10-roadmap.md`. Until M0 lands, the `mise run` tasks below are the contract to implement, not commands that work.

## Commands

Everything runs through mise tasks: `mise run build`, `mise run test`, `mise run lint`, `mise run generate`, `mise run e2e`. If a task is missing, add it to `mise.toml` rather than inventing a command.

Single Go test: `go test ./internal/<pkg> -run '<TestName>'`. `mise run test` should pass extra arguments through to `go test`.

## How it fits together

1. An issue starts: the control plane starts the generic Temporal `Interpret` workflow, with the project's YAML workflow definition snapshotted into the workflow input.
2. Activities launch one Kata pod per agent step. The in-pod runner talks gRPC to the control plane (`Start`, `Prompt`, `Steer`, `Abort`, `Events`) and wraps the harness (Claude Code, opencode, pi). Model traffic goes through LiteLLM with a per-run virtual key.
3. The agent pushes to `agent/*`. Git history is the workspace handoff between steps.
4. Gates: `command` gates re-run in a fresh pod from the pushed SHA; `agent-verdict` and `human` gates follow. `onFail` can loop back to an earlier step, bounded by `maxLoops`.
5. A per-repo merge-queue workflow (signal-with-start) rebases, re-gates and merges.
6. A Release (a SHA plus the issues it contains) moves through project-defined Stages via out-of-process `ReleaseDriver` gRPC plugins, executed only by control-plane Activities.

Details: workflows and gates `docs/03`, runner and harnesses `docs/04`, sandbox `docs/05`, merge queue and releases `docs/06`.

Use the spec's vocabulary in code and docs (defined under Terminology in `docs/00-vision-and-decisions.md`): Issue, Run, Step, Role, Harness, Gate, ChangeRequest (never "PR" in core code), Release, Stage, Driver.

## Stack and conventions

- Go (latest stable pinned in `mise.toml`), standard layout, `internal/` for everything not meant to be imported. Errors wrapped with `%w`. `context.Context` first parameter. No global state. Table-driven tests.
- Postgres with versioned SQL migrations (golang-migrate or goose; decide once in M0 and record in `docs/00`). Queries via sqlc. No ORM.
- Temporal Go SDK for durable workflows.
- gRPC + protobuf (`buf`) for runner and driver interfaces. Generated code is committed.
- UI: React + Bun in `web/`. TypeScript strict.
- Deployment: Helm chart in `deploy/helm/`, Flux manifests in `deploy/flux/`.
- Tooling is declared in `mise.toml`.

## Laptop profile

Every milestone must run on the **laptop profile**: a 16 GiB laptop running single-node k3s with reduced services (see `docs/09-deployment-profiles.md`). Profile differences are configuration, not forks (D18). Check memory budgets and the profile's deferrals before adding a service or a pod.

## Invariants (never violate; changes need explicit human approval)

1. **Agents never receive `kubectl`, cluster credentials, host mounts, or real model API keys.** Sandboxes get a short-lived LiteLLM virtual key and a git token scoped to push to `agent/*` branches only.
2. **Never trust agent-reported results.** Every `command` gate re-runs from the pushed commit in a fresh pod.
3. **Workflow code is deterministic.** Anything non-deterministic (time, randomness, I/O, map iteration order) goes in an Activity. Any change to workflow code needs Temporal versioning (`workflow.GetVersion`) and passing replay tests.
4. **The workflow definition is snapshotted into the workflow input at start.** Never re-read YAML mid-run.
5. **The factory never deploys over the instance doing the work.** See `docs/08-self-hosting.md`.
6. **Protected paths require a human gate**: NetworkPolicies, RBAC, Kata runtime class, runner, gate definitions, LiteLLM budgets, lint/CI config, toolchain files, and the protected-paths list itself. Skipped or deleted tests also trigger the gate.
7. **Control plane core stays provider-neutral.** GitHub specifics live behind `GitProvider` / `IssueSource`. Flux specifics live behind `ReleaseDriver`. No provider types leak into `internal/domain`.
8. **Postgres owns all state except in-flight workflow state, which Temporal owns.** Postgres rows reference Temporal workflow IDs; never duplicate workflow state.

## Decisions and open questions

- Decisions live in the numbered log (D1, D2, ...) in `docs/00-vision-and-decisions.md`. Add new ones at the bottom; never rewrite an entry, supersede it with a new one.
- Before making a choice, check `docs/11-open-questions.md` (e.g. migration tool, secrets management, auth, transcript storage). When you resolve one, record it as a decision in `docs/00` and update `docs/11`.
- Write a decision entry for anything that changes an interface.

## Working agreements

- Small PRs, one issue each. Branch name `agent/<issue-id>/<slug>` when an agent opens it (format not final; see open question 13).
- Every behavior change has a test. Tests for a feature are derived from the spec's acceptance criteria, not from the implementation.
- New workflow behavior gets a replay test fixture.
- New config fields are validated at save time (schema + cross-checks such as harness/model compatibility and driver capabilities).
- Do not add dependencies casually; prefer the standard library. New dependencies need a one-line justification in the PR.
- No emojis in code, docs or commit messages.

## Where things live

See `docs/01-architecture.md` for the repo layout. Short version: `cmd/` binaries, `internal/` packages, `proto/` contracts, `web/` UI, `deploy/` Helm and Flux, `roles/` and `workflows/` example config, `docs/` specs.

## Definition of done for a task

Builds, `mise run lint` and `mise run test` pass, new behavior has tests tagged with the issue ID, docs updated if an interface or invariant changed, no protected paths touched without calling it out in the PR description.
