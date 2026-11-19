# 06 - Delivery: tests, previews, merge queue, releases, flags

Resulting lifecycle:

**build (per issue) > merge queue (per repo) > release stages (per release) > flag rollout (per flag, optional)**

Each stage after the build loop is the same interpreter workflow with a different environment or target.

## E2E test agent

- **FR-TA-1** Runs **in parallel with the coder** and writes tests from the spec and acceptance criteria, **not** from the coder's diff. If it reads the diff, tests mirror the code, bugs included, and stop checking the spec.
- **FR-TA-2** Its tests merge with the issue, so the suite grows with each issue. Staging runs the full suite as the regression check.
- **FR-TA-3** Every test is **tagged with its issue ID**. A staging failure points back to the issue and spec that introduced the behavior; the auto-created bug ticket links both.
- **FR-TA-4** When feature flags are enabled for the project, it writes tests for **both flag states**. Staging runs regression with flags off and new behavior with flags on.
- E2E tests run in the per-PR preview environment before merge (D14). The build loop's deterministic gate additionally verifies the tests compile and lint.
- Implementation (D122): the `e2e-test-writer` role, in the built-in `build-e2e` workflow a project selects with `workflow: build-e2e`. It runs at the same time as the coder on its own branch from the base commit, so it never sees the coder's work; its branch is merged into the issue's once both succeed. A `preserve` gate keeps its tests' lines through the coder's later changes, and a command gate runs `mise run e2e:local`, the project's task that starts the app from the commit in the gate pod and runs the suite against it, before review.

### Running the suite (D116)

A project declares its suite in `.factory/config.yaml`; a stage with `verify: e2e-suite` needs it:

```yaml
e2e:
  command: mise run e2e        # e.g. bru run api --env-var baseUrl=$E2E_BASE_URL --reporter-junit $E2E_RESULTS
  results: e2e-results.xml     # JUnit XML, relative to the repo (default)
```

The control plane runs `command` in a sandboxed gate pod (Kata, read-only git token) at the released commit, checked out from the base branch, in that commit's toolchain image, with `E2E_BASE_URL` (the stage's `url`), `E2E_RESULTS`, `E2E_STAGE` and `E2E_COMMIT`. The stage's host joins the project's egress hosts for the pod. The command's exit code, which the kubelet records, decides the verdict (invariant 2). The JUnit results, which Bruno and Playwright both write and the pod prints to its log, name the failing tests. A test is tagged with the issues whose keys appear in its name or its suite's (FR-TA-3), e.g. a Bruno request `gc-65 search by tag` or a Playwright title with `@gc-65`. A run that wrote no results, could not check out the commit, or whose failing tests all errored (e.g. could not reach the stage) is inconclusive: the stage fails and is rolled back, but no bug issue is opened.

### Preview environments

One ephemeral namespace per change request, via Flux Operator ResourceSets (D14). (Argo CD's ApplicationSet pull-request generator is the equivalent; Flux stays.) The concrete profile allows at most one at a time or none.

Goodreads-Clone's previews run on the homelab, next to its stages (D124, superseding D108 (4)). Frankie's `flux-preview` driver (D125) adds a change request's number and head image tag to a ResourceSet's inputs in `misarescu/Goodreads-CD` by commit, and drops the entries of change requests that merged or closed whenever it next writes; the homelab's Flux renders a namespace `goodreads-pr-<number>` per entry and prunes it when the entry goes. A preview's namespace admits traffic only from one proxy, the tailnet entry point for all previews (`goodreads-previews`), which routes `/pr-<number>/...` to it; Frankie reaches the proxy through one egress Service on Kapsule, so a preview's URL is `http://goodreads-previews.stages.svc.cluster.local/pr-<number>`. A preview holds no secrets, has no egress but DNS and keeps its database on an `emptyDir`. At most `maxPreviews` run at once (default 2): a new one is refused while that many change requests that are still open have previews. Rolling back a preview puts back the change request's previous head, or removes the preview if it had none. A run checks its change request in a preview with the `preview` gate (D126): once the change request is open and its CI passed, the gate deploys the head, waits until the preview runs it, runs the e2e suite against `http://goodreads-previews.stages.svc.cluster.local/pr-<number>` and removes the preview, so a preview lives only for its check. Frankie's own previews (M7.1) run on Kapsule (D131). An entry also holds a slot, `0` to `maxPreviews` - 1: the lowest free one when its preview starts, kept while the entry stays. A ResourceSet derives from it whatever must differ between previews but cannot be named after the change request, such as a NodePort (D134).

## Merge queue

Two change requests can each pass against old `main` and still break when combined, so merges are serialized. The queue is owned by the control plane, independent of git provider tier.

- **FR-MQ-1** One long-running Temporal workflow per repo is the queue. Issue workflows that pass all gates enqueue their ChangeRequest via signal-with-start. One queue workflow per repo means serialized merges with no locking.
- **FR-MQ-2** Processing the head of the queue:
  1. Rebase onto current `main`.
  2. **Clean rebase:** re-run the **deterministic gates only** (build, lint, unit tests). Merge if green.
  3. **Conflict:** return to the coder agent with the conflict context. After resolution, re-run **all gates including agent review** (conflict resolution is new code), then re-enqueue.
  4. **Red after rebase:** treated like a conflict - back to the coder.
- **FR-MQ-3** Order: engineer-defined dependency order, then FIFO.
- Implementation (D87): `MergeQueue` updates a branch by merging the base branch into it for `squash` and `merge` merges (only the final tree reaches the base branch, and a merge conflicts only where the trees do) and by rebasing for `rebase` merges, in a pod that runs only git and pushes with a lease; it re-runs the command gates when the update changed the commit, and merges at exactly that commit while the base branch has not moved. Dependency order needs no queue logic: the scheduler starts an issue only once its dependencies merged (D62). A conflict or a red update goes back to the coder through the enqueue step's `onFail` loop, with both sides of each conflicting file; every step after the loop's target runs again before the change request is re-enqueued (D89). Before merging, the queue also waits for the repo's own CI on the commit; a failure goes back to the coder like a red update (D90), with the end of each failed check's log, bounded by the project's `merge.ciLogs` (D120).
- **FR-MQ-4** Prevention: when choosing parallel issues the scheduler warns if they likely touch the same packages.
- Before the queue (M5, D61): people merge change requests on the provider, and a watcher in `internal/mergequeue` moves the issue from `awaiting_human` to `merged` when it sees the merge. The queue keeps that watcher for merges made by hand.
- **Later optimization** (only if the queue becomes a bottleneck, unlikely at homelab scale): speculative batching - test A, A+B, A+B+C together, merge the longest green prefix.

## Releases and stages

A **Release** is a git SHA plus the set of issues it contains. It moves through an ordered list of **stages** defined per project. Staging, prod and rollout are not built-in concepts; they are one project's configuration.

```yaml
# web service (.factory/config.yaml)
stages:
  - { name: preview, driver: flux-preview, url: "https://pr.example.ts.net", verify: e2e-suite }
  - name: staging
    driver: flux
    config: { repo: misarescu/Goodreads-CD, path: staging }   # the driver's own, checked by it
    url: https://goodreads-stg.example.ts.net
    verify: e2e-suite
    rollback: true                         # requires a driver that can roll back
  - { name: prod,    driver: flux, config: { ... }, gate: human }
  - { name: rollout, driver: flagd }       # only if feature flags are enabled (M8)
```

```yaml
# CLI tool or library (later drivers and verifications)
stages:
  - { name: beta,   driver: github-releases, config: { channel: beta },   verify: smoke-tests }
  - { name: stable, driver: github-releases, config: { channel: stable }, gate: human }
```

A stage (D113) has a `name`, a `driver`, the driver's own `config` (a JSON object), the `url` it serves the project at, `verify` (`e2e-suite`, which needs the `url`, or nothing), `gate` (`human` or nothing), `rollback` (`true` requires it, `false` never, unset when the driver can) and `readyTimeout` (how long the stage may take to run a deployment, from 1 to 25 minutes; the platform's default when unset: 15 minutes for a release, 10 for a preview, D134). The stage whose driver deploys previews, at most one, comes first and has no gate; a release moves through the others in order.

- **FR-RL-1** On merge, a **release agent** prepares the release: semver bump, changelog, release notes, interpretation of verification results. The control plane executes `Publish`, `Promote` and `Rollback` as Temporal Activities behind gates. Agents prepare; they do not deploy.
- **FR-RL-2** **Failure handling**: if a stage's verification fails, the release is marked failed, the stage is rolled back when its driver supports rollback (Flux: revert the env-repo commit), and a **bug issue** linked to the release and its suspect issues is created automatically.
- **FR-RL-3** Capability mismatches are rejected when stage config is saved.

### The release workflow (D114)

Each merge the merge queue makes asks the repo's `Release` workflow (one per repo, `release/<repo>`, signal-with-start from the merge Activity) for a release of its merge commit. One release runs at a time; requests that arrive meanwhile are batched, and the next release is of the newest commit asked for, which contains the others. A release:

1. **Prepare.** Read `stages:` from `.factory/config.yaml` at the commit and snapshot them. No release stages: nothing is released. Stages that do not parse, that their drivers reject, or that need what the deployment cannot do yet (a `human` gate before M8, `verify` without a verifier) record the release as `failed` with the reason. Otherwise record the release with its issues (D110; the merge queue moves an issue to `merged` as it merges, before it asks for the release, D121) and start it.
2. **For each release stage, in order:** record the deployment, `Publish`, ask `Status` until the stage runs the commit (every 10 seconds, at most 15 minutes), then `verify`. The deployment records how the verification ended, with the failed tests (D130).
3. **A stage that fails** (publishing, not running the commit in time, or verification) ends its deployment `failed` with the reason and stops the release. It is rolled back when the stage allows (`rollback`, or the driver can when unset); the release ends `rolled_back`, or `failed` when it was not rolled back or rolling back failed.
   **A failed verification** is a bug in what the release contains, so it also opens a bug issue on the provider (D115). The issue carries the intake label and `bug`, so it lands on the board as a draft bug that runs `bug-fix` once someone starts it. It names the release, its commit, the stage and whether it was rolled back, and the failing tests. It lists the suspects: the release's issues the failing tests are tagged with, or every issue in it when none is. Other failures (publishing, the stage never running the commit) are the stage's and open none.
4. **Every stage passed:** the release `succeeded` and its issues are `released`.

A merge made by a person on the provider (D61) asks for no release; the next merge from the queue releases it with everything else.

### The release agent (D129)

A project opts in with `release: {agent: true}` in `.factory/config.yaml`. Its release workflow then runs the `release-agent` role, read-only, in a sandbox pod, for two jobs:

1. **Prepare.** While the release moves through its stages, the agent reads the release commit, the diff from the last released version, the `## Unreleased` entries of `CHANGELOG.md` and the release's issues. It returns, under the `release-notes` contract, a proposed bump (`major`, `minor` or `patch`) with its reason, and the release notes in Markdown, one entry per issue. The control plane computes the version from the last version a release of the repo got. The first is `v0.1.0`. While the version is below 1.0.0, a breaking change bumps the minor part. When every stage has passed, the control plane tags the release commit `vX.Y.Z` and publishes a provider release (a GitHub Release) with the notes, through the `GitProvider`. A failed or rolled-back release gets no version, and its issues are in the next release, whose agent describes them all. If the agent fails or its answer breaks the contract, the release goes on. The bump then comes from the issue types (only bugs: `patch`; otherwise `minor`), and the notes list the issues' titles with their change requests. Nothing is committed to the repository: `CHANGELOG.md` stays as the coders write it.
2. **Interpret a failed verification.** Before the bug issue of a failed verification is opened (D115), the agent reads the failing tests, the release's issues and their diffs, and returns, under the `release-analysis` contract, the likely culprits with how likely each is and why. The analysis is a section of the bug issue. The suspects from the tests' tags stay as they are. If the agent fails, the issue is opened without the analysis.

The agent's sessions are the steps of a *release run*, a Run that belongs to the release instead of an issue. Their transcripts, cost and tokens are recorded like any step's. Their keys share a per-release budget (`controlPlane.release.agentBudgetUSD`, default 1 USD). The agent never deploys and never pushes (FR-RL-1).

### The flux driver (D117)

```yaml
- name: staging
  driver: flux
  config:
    repo: misarescu/Goodreads-CD                          # the GitOps repository Flux syncs
    path: apps/goodreads/staging/kustomization.yaml       # pins the stage's image
    image: ghcr.io/misarescu/goodreads-clone              # the images entry a release sets
    versionURL: http://goodreads-stg.192.168.3.7.nip.io/api/version
    # branch: main, tag: "sha-{short}", versionField: version (defaults)
```

`Publish` commits the release's tag (`newTag` of `image`) to `path` on `branch` with the provider credential, editing only that line, and names the deployment in the commit message; a stage that already pins the tag is left alone. `Status` is ready once `versionURL` reports the release's commit (its full SHA or a prefix of at least 7). `Rollback` commits the previous tag back, only while the stage still pins the release's tag. Capabilities: rollback and health.

### Where stages run (D108)

For Goodreads-Clone, the M7 proving project:

| Stage | Cluster | Deployed by | Verified by |
|---|---|---|---|
| preview (per change request) | the homelab (D124) | the homelab's Flux, from ResourceSet inputs Frankie's `flux-preview` driver commits to `misarescu/Goodreads-CD` | its e2e tests against the preview's URL, before merge |
| staging | homelab | the homelab's Flux from `misarescu/Goodreads-CD` (`flux`) | `/api/version` reports the release's commit, then the e2e suite against the stage's URL |
| prod | homelab | the same, behind a human gate | as staging |

Frankie holds no credential for the homelab: it changes a stage only by committing the stage's image tag to the GitOps repository through the `GitProvider`, rolls back by reverting that commit, and reaches the stage's URL over Tailscale (D118). On the tailnet `tail9cf1a9.ts.net` each stage is its own device (`goodreads-stg`, `goodreads`, tagged `tag:goodreads-stage`), which the Tailscale operator on Kapsule reaches through an egress Service per stage: `http://goodreads-stg.stages.svc.cluster.local` is staging's URL there, for the control plane (the `flux` driver's `versionURL`) and for verification pods alike.

### Release driver interface

```go
// internal/drivers (D112); the gRPC service is proto/frankie/driver/v1/driver.proto.
type Driver interface {
    Name() string               // how stage configuration names it, e.g. "flux"
    Capabilities() Capabilities // Rollback, Progressive, Health, Preview
    ValidateConfig(ctx context.Context, config json.RawMessage) error // at save time
    Publish(ctx context.Context, r Request) (State, error)  // returns once the change is made
    Status(ctx context.Context, r Request) (Status, error)  // progressing, ready or failed
    Promote(ctx context.Context, r Request, step RolloutStep) (State, error) // only if Progressive
    Rollback(ctx context.Context, r Request) (State, error)                 // only if Rollback
}
// Request: the Stage (name, driver config), the Subject (repo, commit, and a
// release or, for a preview, a change request) and the Deployment (ID, State).
// State is the driver's own JSON object, stored on the Deployment and handed back.
```

- The contract is a **gRPC service** (`proto/driver.proto`), so a driver can be an **out-of-process plugin** (hashicorp/go-plugin, like Terraform providers) and a new target never touches the control plane. M7's built-in drivers implement the same interface in-process; the plugin host comes with the first driver built outside this repository (D109). Either way drivers run only in control-plane Activities.
- **First drivers**: `flux`, `flux-preview`, `flagd`. Later candidates: Argo CD, GitHub Releases, Helm chart/OCI registries, npm/PyPI/Go module tags, update feeds.
- **Executors**: all steps (builds, verification) run in Kubernetes pods for now, so supported projects are limited to what the cluster can build. A later `runsOn:` field can target external executors (e.g. macOS/Windows hosts for signing and notarization).

## Feature flags (optional, per project)

Use flags instead of release branches. Release branches mean cherry-picks, back-merges and two lines of code to keep consistent, which agents handle badly, and they break "main is the truth, a release is a SHA". Flags separate deploying from releasing: `main` is always deployable and choosing what users see is switching a flag. A test pool of users is a targeting rule. **Enable flags when a project starts**; retrofitting leaves every earlier issue unflagged.

Web services benefit most; a desktop app or plugin may skip flags and use channel promotion (beta > stable).

Stack:

- **OpenFeature** SDKs (Go, JS, React) so code never depends on one vendor.
- **flagd** as the provider (self-hosted OpenFeature reference implementation). Flag definitions are **files in the GitOps repo**, so flag changes go through Flux, are reviewed and can be reverted. Agents can propose rollouts (e.g. "enable X for beta group") as commits. Unleash or GrowthBook can replace flagd behind the same interface if a UI with targeting/analytics is needed.

Effect on the factory when enabled for a project:

- **Spec template** has a flag field (name for features, none for bug fixes). The coder role wraps code in it; the reviewer checks it.
- **Rollout stage** with the `flagd` driver: internal users > test pool > percentage > 100%, each step a gate (usually human).
- **Automatic flag cleanup**: when a flag stays at 100% for N days, the system opens a "remove flag X" issue for the agents. Without this, flag debt accumulates quickly when agents write the code.
