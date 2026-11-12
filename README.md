# Frankie Software Factory

Frankie is a self-hosted platform that turns specs into merged, released software using role-based coding agents. It runs on a homelab Kubernetes cluster, is configured per project (not hardcoded), and is delivered by GitOps (Helm + FluxCD).

The name comes from the shipwright of the Strawhats crew from One Piece. Frankie can build and maintain everything, you just have to ask.

Status: **M0 scaffold. No product code yet.** Start with `AGENTS.md`, then `docs/spec/10-roadmap.md`.

Getting started: install [mise](https://mise.jdx.dev), then `mise install` and `mise run doctor`.

## What it does

1. An engineer writes **issues** (specs). They can be grouped into epics, stories and tasks and have a dependency graph.
2. The engineer starts one issue or many. There are no sprints: the engineer sets the order or lets independent issues run in parallel.
3. Each issue runs through a **configurable workflow**: coder agent and e2e test agent in parallel, deterministic gates, reviewer agents, then a **merge queue**.
4. Merged work becomes a **release** (a git SHA plus the issues it contains) that moves through project-defined **stages** (preview, staging, prod, flag rollout) via pluggable **release drivers**.
5. Everything is observable live and replayable; humans can ask, steer, pause or take over.

## Design principles

- **Control plane is separate from the agent loop.** We do not write an agent loop. We wrap existing harnesses (Claude Code, opencode, pi) behind one runner interface.
- **The workflow is data.** One generic interpreter runs a YAML workflow definition; "factory flavors" are configuration.
- **Never trust the agent.** Deterministic gates re-run everything in a fresh pod from the pushed commit.
- **Agents never get `kubectl`, real API keys, or host access.** Deployment is executed by the control plane through release drivers, behind gates.
- **`main` is always deployable.** A release is a SHA. Feature flags (not release branches) decide what users see.
- **Humans stay in control** at: spec approval, protected-path changes, production release, and any promotion of the factory itself.

## Stack

Go (control plane, runner, CLI), Postgres, Temporal, Kubernetes (k3s on the laptop profile) with Kata Containers, Helm, FluxCD, LiteLLM, OpenFeature + flagd, OpenTelemetry. UI: React, Bun.

## Document map

| File | Contents |
| --- | --- |
| `CLAUDE.md` / `AGENTS.md` | Working rules and invariants for coding agents in this repo |
| `docs/notes/` | Working notes (M0 exploration) |
| `docs/spec/00-vision-and-decisions.md` | Goals, non-goals, decision log |
| `docs/spec/01-architecture.md` | Components, boundaries, repo layout |
| `docs/spec/02-domain-model.md` | Entities, state machines, Postgres ownership |
| `docs/spec/03-workflows-and-gates.md` | Workflow YAML schema, interpreter, gates, roles |
| `docs/spec/04-agents-harnesses-models.md` | Runner interface, harness adapters, LLM gateway, budgets |
| `docs/spec/05-sandbox-and-toolchains.md` | Pod sandbox, network, credentials, toolchain images |
| `docs/spec/06-delivery.md` | E2E test agent, previews, merge queue, release stages, drivers, feature flags |
| `docs/spec/07-observability.md` | Event stream, views, metrics, human intervention |
| `docs/spec/08-self-hosting.md` | The factory building itself safely |
| `docs/spec/09-deployment-profiles.md` | Full profile vs laptop profile (sizing, deferrals, checklist) |
| `docs/spec/10-roadmap.md` | Milestones with acceptance criteria |
| `docs/spec/11-open-questions.md` | Unresolved decisions and spec inconsistencies |
| `docs/spec/12-landscape.md` | Existing projects worth studying |
