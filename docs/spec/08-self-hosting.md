# 08 - Self-hosting (the factory builds itself)

Yes, and it is the strongest test of the design. The factory is a Go/React/Postgres/Helm repo, which matches its own toolchain and workflow model. But software that changes and deploys itself has failure modes an ordinary repo does not.

## Bootstrapping

Like bootstrapping a compiler: build the first version by hand (Claude Code locally, normal CI) up to roadmap milestone **M3** (one issue end to end, configurable workflows and gates). From then on the factory's own repo is one more project on its board and new features are specs handed to it. Before M3 it cannot do enough to be worth pointing at itself.

## Main risk: breaking the thing that fixes things

If the factory ships a bad change to its own control plane, it can no longer run the workflow that would fix it. Rules:

- **FR-SH-1** **The factory never deploys over the instance doing the work.** Two instances: a stable `factory-prod` builds the next version; its staging target is a separate `factory-staging` instance, never itself.
- **FR-SH-2** **Promoting the factory itself always needs a human gate.** No auto-promotion for this repo, even where other projects allow it.
- **FR-SH-3** **Recovery does not depend on the factory.** Rollback is a revert commit in the GitOps repo, which Flux applies whether or not the factory runs. A break-glass path exists: the repo builds with plain CI and can be worked on with Claude Code locally. The factory is the normal path, not the only one.

## Gates specific to building the factory

- **Temporal replay tests** (most important). A change to workflow code that breaks determinism breaks in-flight workflows on deploy. A `command` gate replays recorded workflow histories against the new code with Temporal's replayer. The repo's agent docs state that workflow changes must use Temporal versioning (`GetVersion` or worker versioning); agents will not know this unaided and reviewers can miss it.
- **Guardrails are protected paths.** An agent working on the factory edits its own security boundaries. NetworkPolicies, RBAC, the Kata runtime class, the runner, gate definitions, gateway budgets and the protected-paths list always require a human gate.
- **The factory's e2e test is "can it build something?"** The preview environment deploys a complete factory (Temporal dev server, Postgres, runner) into the PR namespace, then runs a **canary issue** end to end against a toy repo with a cheap model and a fixed spec, checking that a PR comes out green. Unit tests cannot show the factory still works; building something does.

## What it gives back

The best dogfooding signal available. If the factory struggles with its own issues (long loops, failed gates, weak specs), the metrics show where to improve. Several of its roles (Go coder, React coder, Helm reviewer, security reviewer) match the stack in other projects, so tuning them for the factory tunes them everywhere.
