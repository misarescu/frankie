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

### Preview environments

One ephemeral namespace per change request from the start, via Flux Operator ResourceSets with a GitHub pull request input provider. (Argo CD's ApplicationSet pull-request generator is the equivalent; Flux stays.) Limits on concurrent previews and TTL are configuration (open question); the concrete profile allows at most one at a time or none. Since M7 previews run on Frankie's Kapsule cluster, not where the project's stages run (D108).

## Merge queue

Two change requests can each pass against old `main` and still break when combined, so merges are serialized. The queue is owned by the control plane, independent of git provider tier.

- **FR-MQ-1** One long-running Temporal workflow per repo is the queue. Issue workflows that pass all gates enqueue their ChangeRequest via signal-with-start. One queue workflow per repo means serialized merges with no locking.
- **FR-MQ-2** Processing the head of the queue:
  1. Rebase onto current `main`.
  2. **Clean rebase:** re-run the **deterministic gates only** (build, lint, unit tests). Merge if green.
  3. **Conflict:** return to the coder agent with the conflict context. After resolution, re-run **all gates including agent review** (conflict resolution is new code), then re-enqueue.
  4. **Red after rebase:** treated like a conflict - back to the coder.
- **FR-MQ-3** Order: engineer-defined dependency order, then FIFO.
- Implementation (D87): `MergeQueue` updates a branch by merging the base branch into it for `squash` and `merge` merges (only the final tree reaches the base branch, and a merge conflicts only where the trees do) and by rebasing for `rebase` merges, in a pod that runs only git and pushes with a lease; it re-runs the command gates when the update changed the commit, and merges at exactly that commit while the base branch has not moved. Dependency order needs no queue logic: the scheduler starts an issue only once its dependencies merged (D62). A conflict or a red update goes back to the coder through the enqueue step's `onFail` loop, with both sides of each conflicting file; every step after the loop's target runs again before the change request is re-enqueued (D89). Before merging, the queue also waits for the repo's own CI on the commit; a failure goes back to the coder like a red update (D90).
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

A stage (D113) has a `name`, a `driver`, the driver's own `config` (a JSON object), the `url` it serves the project at, `verify` (`e2e-suite`, which needs the `url`, or nothing), `gate` (`human` or nothing) and `rollback` (`true` requires it, `false` never, unset when the driver can). The stage whose driver deploys previews, at most one, comes first and has no gate; a release moves through the others in order.

- **FR-RL-1** On merge, a **release agent** prepares the release: semver bump, changelog, release notes, interpretation of verification results. The control plane executes `Publish`, `Promote` and `Rollback` as Temporal Activities behind gates. Agents prepare; they do not deploy.
- **FR-RL-2** **Failure handling**: if a stage's verification fails, the release is marked failed, the stage is rolled back when its driver supports rollback (Flux: revert the env-repo commit), and a **bug issue** linked to the release and its suspect issues is created automatically.
- **FR-RL-3** Capability mismatches are rejected when stage config is saved.

### Where stages run (D108)

For Goodreads-Clone, the M7 proving project:

| Stage | Cluster | Deployed by | Verified by |
|---|---|---|---|
| preview (per change request) | Frankie's Kapsule cluster | Flux Operator ResourceSets there (`flux-preview`) | its e2e tests against the preview's URL, before merge |
| staging | homelab | the homelab's Flux from `misarescu/Goodreads-CD` (`flux`) | `/api/version` reports the release's commit, then the e2e suite against the stage's URL |
| prod | homelab | the same, behind a human gate | as staging |

Frankie holds no credential for the homelab: it changes a stage only by committing the stage's image tag to the GitOps repository through the `GitProvider`, rolls back by reverting that commit, and reaches the stage's URL over Tailscale (open question 20).

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
