# 10 - Roadmap

Ordered to get a working end-to-end slice early, then layer the product. Steps 2 and 3 alone reproduce most of a typical work setup (sandboxed, tracker-agnostic); everything after is the product. Each milestone lists acceptance criteria that can be turned into issues and e2e tests. Milestones are specs for the factory itself and, after M3, are candidates to be run by the factory (see `08-self-hosting.md`).

Every milestone must run on the **concrete profile** (see `09-deployment-profiles.md`).

## M0 - Explore and scaffold (no product code)

- Run Vibe Kanban and Gas City against a toy repo; record what to keep and what annoys you in `docs/notes/exploration.md`.
- Repo skeleton per `01-architecture.md`: Go module, `mise.toml` with tasks `build test lint generate e2e`, buf config, sqlc config, migration tool choice recorded in `docs/spec/00`, CI running `mise run lint test`.
- Helm chart skeleton and Flux manifests for a k3s dev cluster.
- **Done when:** `mise run build lint test` is green on an empty-but-structured repo in CI, and the exploration notes exist.

## M1 - One issue, end to end

Go service takes a GitHub issue and produces a PR from a Claude Code run in a sandboxed pod.

- Control plane with Postgres store (Issue, Run, StepRun, Event), minimal API.
- Temporal (single binary) with a hardcoded minimal workflow: launch agent pod, wait, open ChangeRequest.
- Runner (gRPC `Start/Prompt/Abort/Events`) wrapping Claude Code stream-JSON.
- Pod sandbox: Kata runtime class, NetworkPolicy plus egress proxy (D20), git proxy with a per-step token limited to `agent/<issue-key>/*` (D21), no service-account token.
- LiteLLM gateway with per-run virtual key and budget (real keys never in the pod).
- `GitProvider` + `IssueSource` for GitHub.
- Live log streaming to a minimal UI page (Postgres LISTEN/NOTIFY).
- **Done when:** labeling a GitHub issue `factory` on a toy repo results in a PR on `agent/<issue-key>/*`, with the transcript streaming live and replayable afterwards, within the concrete profile's memory budget, and an attempt to push to `main` or reach a non-allowlisted host from the pod fails (tested).

## M2 - Toolchains and deterministic gates

- Platform base image `factory-base:v1`; per-repo `mise.toml`; image hash/build/push to `registry:2` via one-off BuildKit Job.
- `command` gate: fresh pod from the pushed commit running `mise run lint` / `mise run test`.
- Sidecar services declared in `.factory/config.yaml`.
- **Done when:** a gate fails on a commit whose agent claimed success but whose tests fail, and the image for an unchanged `mise.toml` is reused rather than rebuilt (tested).

## M3 - Configurable workflows and gates

- Generic `Interpret` workflow, YAML schema, save-time validation, snapshot at start (`03-workflows-and-gates.md`).
- Step kinds: agent, gate, parallel, action. Gate types: `command`, `agent-verdict`, `human`. Policies and `onFail` loops with `maxLoops`.
- Roles as versioned files; `coder`, `code-reviewer` roles; JSON verdict schema.
- Protected-path and test-removal detection forcing human gates.
- Temporal replay tests in CI.
- **Done when:** the example `default-build` workflow runs end to end including a reviewer-triggered loop back to `implement` and a human-gated protected-path change; replay tests pass. **The factory repo is onboarded as a project (self-hosting begins).**

## M3.1 - Dependency images (build reuse)

Agent steps and gates start every pod with empty dependency and build caches, so a repo's dependencies are downloaded and compiled on every run: Frankie's first gate (frk-1) took 17 minutes, most of it fetching Go modules and compiling dependencies, twice (once for `lint`, once for `test`). M3.1 bakes a repo's dependencies into an image layer, in a way that works for any ecosystem, not only Go.

- `dependencies` in `.factory/config.yaml`: the files the warm-up reads (globs relative to the repo root, e.g. `go.mod`, `go.sum`, `**/pom.xml`, `build.gradle.kts`, `uv.lock`, `requirements*.txt`, `**/*.csproj`, `packages.lock.json`, `conanfile.txt`, `web/bun.lock`) and a `run` command, normally a mise task, that downloads them and pre-compiles them where the ecosystem allows. The platform has no per-language logic; the repo decides what to warm (docs/spec/05).
- **Dependency image**: `FROM` the repo's toolchain image by digest, with the declared files at their repo paths and the warm-up run as the agent user with the sandbox environment, so every ecosystem's cache lands where it looks at run time. Tagged by a hash of the toolchain digest and the files; built, verified and recorded by digest like toolchain images (D45). Agent steps and gates run it in place of the toolchain image. Repos without `dependencies` keep today's behavior.
- Changed dependency files rebuild only this layer (minutes), not the toolchain (about 13 minutes for Frankie); unchanged files reuse it.
- Caches live only in image layers built from the declared files; whatever an agent or gate pod writes stays in that pod, so no run can poison another's cache (invariant 2).
- Hosts a warm-up needs (Maven Central, NuGet, ...) go through the egress allowlist as usual.
- **Done when:** Frankie's gate with unchanged dependencies takes at most 6 minutes on the concrete profile (17 at M3); unchanged dependency files reuse the dependency image and a changed `go.sum` rebuilds only the dependency layer (tested); a non-Go toy project (Python or Java) gets a working dependency image from its own declaration (e2e).

## M4 - Harnesses and models

- opencode and pi adapters in the runner (ACP where it proves mature); normalized events across all three.
- Provider profiles vs model refs; harness/model compatibility validation.
- Per-step model selection; budgets per step/issue; cost and token metrics.
- Steer, pause/abort, ask-human tool; web terminal take-over.
- **Done when:** one workflow mixes all three harnesses and two providers, and a steer message changes the behavior of a running session in each.

## M5 - Issues and board

- Native issue store with epic/story/task/bug hierarchy, DAG, ordering, statuses, sync from GitHub Issues.
- Scheduler: start one or many, honor DAG and concurrency, overlap warning.
- Refinement agent and spec-approval gate (draft > refining > ready).
- UI: board/list with DAG, issue detail, approvals inbox.
- **Done when:** starting an epic runs its tasks in dependency order and in parallel where independent (concurrency 1 on the concrete profile, 2+ on the full profile), blocked on spec approval where required.

## M6 - Merge queue

- Per-repo queue workflow, signal-with-start enqueue, rebase/conflict/red handling (`06-delivery.md`).
- Auto-merge once gates pass.
- **Done when:** two issues editing overlapping code both reach `main` with the second being sent back to the coder on conflict and re-reviewed; `main` is never red.

## M7 - E2E test agent, previews, release stages

- `e2e-test-writer` role, tests tagged with issue IDs, both-flag-state support hook.
- Flux Operator ResourceSets preview per change request (limit 1 on the concrete profile).
- `Release` entity, `Stage` config, `ReleaseDriver` gRPC plugin interface, `flux` and `flux-preview` drivers, verification with the e2e suite, rollback by revert, auto-created bug issues.
- Release agent (semver, changelog, notes).
- **Done when:** a merged issue becomes a release that deploys to staging, a deliberately broken release is rolled back and produces a bug issue linking release and suspect issue, and the factory's own canary e2e (`08-self-hosting.md`) runs in the preview environment.

## M8 - Production and flag rollout

- `prod` stage with human gate; `flagd` driver and OpenFeature in the spec template; rollout steps with gates; automatic flag-cleanup issues.
- Optional `factory-driver` MCP server with scoped tools.
- Further drivers as needed (GitHub Releases first).
- **Done when:** a flagged feature rolls internal > test pool > percentage > 100% through gates and generates a cleanup issue after N days at 100%.

## Later

Speculative merge batching; external executors (`runsOn:`); Argo CD / registry drivers; PVC-per-issue workspaces; multi-user auth and RBAC; Unleash/GrowthBook behind OpenFeature; full-profile services (NATS, Nexus, Harbor/Zot, Tempo/Loki).
