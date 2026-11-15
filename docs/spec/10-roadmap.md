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
- **Done (2026-11-13):** on the homelab, Goodreads-Clone gc-5 ran `default-build` with its reviewer failing the first commit and the run looping back to `implement` (Goodreads-Clone#16); Frankie's first own issue, frk-1, passed its own lint and tests (with the replay tests) and review, then waited at the forced `protected-changes` gate for its `roles/` change until a person approved it in the inbox (frankie#37). Nine replay fixtures pass in CI.

## M3.1 - Dependency images (build reuse)

Agent steps and gates start every pod with empty dependency and build caches, so a repo's dependencies are downloaded and compiled on every run: Frankie's first gate (frk-1) took 17 minutes, most of it fetching Go modules and compiling dependencies, twice (once for `lint`, once for `test`). M3.1 bakes a repo's dependencies into an image layer, in a way that works for any ecosystem, not only Go.

- `dependencies` in `.factory/config.yaml`: the files the warm-up reads (globs relative to the repo root, e.g. `go.mod`, `go.sum`, `**/pom.xml`, `build.gradle.kts`, `uv.lock`, `requirements*.txt`, `**/*.csproj`, `packages.lock.json`, `conanfile.txt`, `web/bun.lock`) and a `run` command, normally a mise task, that downloads them and pre-compiles them where the ecosystem allows. The platform has no per-language logic; the repo decides what to warm (docs/spec/05).
- **Dependency image**: `FROM` the repo's toolchain image by digest, with the declared files at their repo paths and the warm-up run as the agent user with the sandbox environment, so every ecosystem's cache lands where it looks at run time. Tagged by a hash of the toolchain digest and the files; built, verified and recorded by digest like toolchain images (D45). Agent steps and gates run it in place of the toolchain image. Repos without `dependencies` keep today's behavior.
- Changed dependency files rebuild only this layer (minutes), not the toolchain (about 13 minutes for Frankie); unchanged files reuse it.
- Caches live only in image layers built from the declared files; whatever an agent or gate pod writes stays in that pod, so no run can poison another's cache (invariant 2).
- Hosts a warm-up needs (Maven Central, NuGet, ...) go through the egress allowlist as usual.
- **Done when:** Frankie's gate with unchanged dependencies takes at most 6 minutes on the concrete profile (17 at M3); unchanged dependency files reuse the dependency image and a changed `go.sum` rebuilds only the dependency layer (tested); a non-Go toy project (Python or Java) gets a working dependency image from its own declaration (e2e).
- **Done (2026-11-13):** Frankie's frk-2 ran in its dependency image: gate 4m46s (frk-1: 17m18s), agent step 5m38s (14m12s) (frankie#44). Activity tests cover reuse and the `go.sum`-only rebuild; the `dependencies` e2e builds and runs a Python toy's dependency image. Building a dependency image is slow (Frankie's: 29m44s), almost all of it BuildKit unpacking and copying the toolchain image inside the Kata VM (open question 16 d).

## M4 - Harnesses and models

- opencode and pi adapters in the runner (ACP where it proves mature); normalized events across all three.
- Provider profiles vs model refs; harness/model compatibility validation.
- Per-step model selection; budgets per step/issue; cost and token metrics.
- Steer, pause/abort, ask-human tool. Pause is deferred (D52): no harness pauses natively, and a steer or an abort covers its uses.
- Web terminal take-over moves to after open question 7 (authentication): a shell into an agent pod through an unauthenticated API is a larger risk than deciding a gate.
- Second provider on the concrete profile: OpenRouter, next to Anthropic.
- **Done when:** one workflow mixes all three harnesses and two providers, and a steer message changes the behavior of a running session in each.
- **Done (2026-11-14):** Goodreads-Clone's gc-6 ran `m4-mix` on the concrete profile: implement on Claude Code (`anthropic/claude-sonnet-5-5`), document on opencode (`openrouter/openai/gpt-5.6-luna`), lint and test, review on pi (`openrouter/openai/gpt-5.6-terra`), one review loop back, then the change request (Goodreads-Clone#20); 39 minutes including a toolchain rebuild, $0.47. A steer changed each session's output: Claude Code added a `Cache-Control: no-store` header, opencode a given CHANGELOG line, pi a given opening to its verdict's summary. Claude Code and opencode asked through the ask-human tool and used the answers from the inbox. The run found two gaps, fixed after it: answers did not reach later steps, so the reviewer failed a choice a person had made (D56, frankie#57), and input token totals left out cached input (frankie#56).

## M5 - Issues and board

- Native issue store with epic/story/task/bug hierarchy, DAG, ordering, statuses, sync from GitHub Issues.
- Scheduler: start one or many, honor DAG and concurrency, overlap warning.
- Refinement agent and spec-approval gate (draft > refining > ready).
- UI: board/list with DAG, issue detail, approvals inbox.
- **Done when:** starting an epic runs its tasks in dependency order and in parallel where independent (concurrency 1 on the concrete profile, 2+ on the full profile), blocked on spec approval where required.
- **Done (2026-11-14):** Goodreads-Clone's epic #22 (gc-7) with three sub-issues, the frontend (gc-9) and the docs (gc-10) both blocked by the API (gc-8), and the docs labeled `needs-spec-approval`, synced in as drafts with their hierarchy and dependencies (D57, D60). Starting the epic from the board on the concrete profile (`maxConcurrentIssues` 1) ran gc-8 (Goodreads-Clone#26). Its slot freed when the run ended, so gc-10's refinement ran while the change request waited for a person. The refiner asked a question through the ask-human tool, which was answered from the inbox, and the refined spec was approved there (D64-D66). The merge of #26, seen by the merge watcher (D61), released gc-9 (#27) and then gc-10 (#28), one at a time; every gate passed on the first attempt; four runs, $0.60 in all. Parallel starts with two or more slots are covered by tests (`domain.PlanSchedule`, and the scheduler against the store with a limit of 2) rather than live, since the homelab runs the concrete profile. Found by the run, for later: the refiner works without seeing its dependencies' code, so gc-10's spec called itself blocked (give the refiner its dependencies' specs and change requests); a parent's status stays `draft` (derive it from its children); the scheduler logs no requester when the request came through a parent; and decisions are recorded as `web` until per-person identity (open question 7).

## M5.1 - Issue lifecycle and run visibility

Fixes from the M5 acceptance run and its review, before the merge queue.

- Readable runs: events labeled in plain words instead of their types, each attributed to its step (implement, checks, review, ...) and to the role, harness and model that produced it; runs listed with their ID and date on the issue page.
- Live view on the issue page: the current run's step timeline with status and duration, and the agent's latest messages and tool calls, updating while the run goes; the full transcript stays on the run page.
- Closing issues: a parent shows its progress (merged children of all) and stays open by default, since issues may be added to it later. A person completes a parent whose children are all finished (it moves to `merged`) or cancels any unfinished issue with a reason (`canceled`). Frankie then closes the source issue too, as completed or as not planned, with a comment naming who closed it: the first write-back through `IssueSource`.
- The refiner sees the issue's dependencies: their specs, statuses and change requests, so a dependent spec is written against the work it builds on.
- The scheduler logs who asked to start an issue when the request came through a parent; the UI asks for a name once and records it on decisions and answers instead of `web` (still unauthenticated, open question 7).
- **Done when:** a Goodreads-Clone epic with stories that have their own tasks, and a bug, runs from the issue list with its graph and live view, the epic is completed from Frankie and closes on GitHub, and an issue is canceled from Frankie and closes on GitHub as not planned.
- **Done (2026-11-15):** Goodreads-Clone's "Yearly reading challenge" (gc-13, #32: two stories with five tasks and a bug, a blocked-by chain, one task needing spec approval) ran from the issue list with its graph and live view; the refined task's refiner saw its dependency's change request (D69). Its email-reminders task (gc-21, #39) was canceled from Frankie and closed on GitHub as not planned with a comment naming who and why; the stories and the epic were completed from Frankie and closed as completed (D70, D71), as were the M5 epic gc-7 (#22) and gc-11 (#29). Six runs, $1.45. A second epic, "Book discovery" (gc-23, #48: three independent stories, six tasks), ran with two scheduler slots to measure pods running in parallel: $2.25, all merged and completed. The run produced the readable-run, live-view, layout and parent-page work in D67-D74 and D76 from the user's feedback during it, and found what M5.2 and M6 now list: a toolchain build failing on a missing egress host (`nodejs.org`, fixed in the allow-list), a second run sharing that build failing with "job not found", lost build logs, slow image builds, CPU requests that kept a second agent `Pending` on a mostly idle node, a refinement held after an infrastructure failure, two change requests conflicting after parallel work (resolved by hand), and a repo whose own CI disagreed with Frankie's gate (Goodreads-Clone pinned Node in `mise.toml` and moved CI onto it).

## M5.2 - Sandbox and builds

Fixes found by the M5.1 acceptance run, before the merge queue: projects configure their sandbox, builds get faster and visible when they fail, and pods are sized from measurements.

- Per-project egress: a project adds hosts to the platform's allow-list in `.factory/config.yaml` (`egress.allow`), read from the base branch at start (invariant 4) and protected (invariant 6). Plain hostnames only, validated; the deployment can refuse project additions (`egressProxy.projectAllow`). The egress proxy learns each pod's project from a short-lived signed token, like the git proxy, and checks the destination against that project's list. The Helm list stays the baseline.
- Shared builds: runs waiting for the same image build see its outcome; one run's failed attempt no longer leaves another with "job not found".
- Failed build logs are kept (a failed build's last lines reach the run's transcript), so a broken build says why.
- Faster builds: measured on Goodreads-Clone's toolchain build (11 min: unpacking the base image 6 min 13 s, copying a 137-byte `mise.toml` 1 min 46 s, `mise install` 2 min 13 s of which downloads 28 s, exporting 1 min 54 s). Try, measure and pick: BuildKit state on a block volume or a registry cache, appending a tools layer to the base image's manifest instead of rebuilding it, cheaper compression. Measured (`docs/notes/m5.2-build-measurements.md`): BuildKit falls back to its native snapshotter on the Kata VM's virtiofs, which copies the whole tree for every snapshot; appending a layer built in the base image itself took 62-83 s for the toolchain (from 696 s) and 103 s for the dependency image (from about 25 min), BuildKit on tmpfs 99 s and 150 s at 3-4 GiB of RAM, a PVC or a registry cache barely helped. The registry (128 MiB) was OOM-killed receiving a dependency image.
- Pod sizing from the M5.1 samples (5 s samples over both epics, `kubectl top`): agent pods used a median 0.86 CPU (p90 1.81, max 1.86) and 1.0 GiB (max 2.6 GiB); gate pods are CPU-bound (median 1.81 CPU, memory under 1 GiB); build pods are I/O-bound (median 0.15 CPU) with memory up to 2.87 GiB against their 3 GiB limit. The node never used more than 2.5 of its 4 CPUs or 6 GiB of 15 GiB, yet 12 pods waited `Pending` for a minute or more (42 minutes in all, up to 8 min for a gate) because every pod requested 2 CPUs. Separate CPU requests from limits (e.g. agents 500m, reviewers 250m, gates 1 CPU, builds 250m, limits unchanged), keep memory requests equal to limits, and raise the build memory limit.
- A refinement or run that fails before its agent starts (image build, pod) does not hold the draft (D66 holds it only for a bad refiner result), and an issue page offers "Refine again" on a held draft, also when a parent's request covers it.
- **Done when:** a Goodreads-Clone epic with independent tasks runs with two slots without a pod waiting on CPU requests, a toolchain change rebuilds in a fraction of today's 11 minutes, a project adds an egress host in its own config, and a failed build shows its error on the issue.

## M6 - Merge queue

- Per-repo queue workflow, signal-with-start enqueue, rebase/conflict/red handling (`06-delivery.md`).
- Auto-merge once gates pass, and once the change request's provider checks are green too (`GitProvider.CommitStatus`): in the M5.1 run a repo's own CI failed where Frankie's gate passed.
- Conflicts go back to the coder with both sides: in the M5.1 run they were in registry-style files (repository interfaces, struct fields, mock config, imports in `App.jsx`) where keeping both is right.
- Approve and reject a change request from Frankie: approving merges it (through the queue), rejecting closes it and sends the issue back with the reason (resolves open question 11). An issue moves to `awaiting_human` while a gate or question inside its run waits, without the merge watcher recording a merge under a running workflow (D76).
- Visibility: a step waiting for an image says it is building the toolchain or dependency image, and the issue page streams the build's log; a gate that passed while the provider's CI failed on the same commit is shown on the issue.
- Bug-fix workflow, built in next to `default-build` and selected by issue type (`workflows: {bug: bug-fix}`): a read-only `investigate` step with a findings contract (root cause, evidence, how to reproduce, whether a test can), a human gate only when no test can reproduce it (approve, send back with feedback, or cancel), a `reproduce` step that pushes only a failing test, a command gate that must fail, `fix` without touching that test, checks, a review that checks root cause and the test, and a change request naming the root cause. Needs conditional gates, `expect: fail` command gates and per-type workflow selection.
- **Done when:** two issues editing overlapping code both reach `main` with the second being sent back to the coder on conflict and re-reviewed; `main` is never red; a change request is approved and merged from Frankie; a Goodreads-Clone bug runs the bug-fix workflow from investigation to a merged fix with its reproducing test.

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
