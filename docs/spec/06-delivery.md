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

One ephemeral namespace per change request from the start, via Flux Operator ResourceSets with a GitHub pull request input provider. (Argo CD's ApplicationSet pull-request generator is the equivalent; Flux stays.) Limits on concurrent previews and TTL are configuration (open question); the concrete profile allows at most one at a time or none.

## Merge queue

Two change requests can each pass against old `main` and still break when combined, so merges are serialized. The queue is owned by the control plane, independent of git provider tier.

- **FR-MQ-1** One long-running Temporal workflow per repo is the queue. Issue workflows that pass all gates enqueue their ChangeRequest via signal-with-start. One queue workflow per repo means serialized merges with no locking.
- **FR-MQ-2** Processing the head of the queue:
  1. Rebase onto current `main`.
  2. **Clean rebase:** re-run the **deterministic gates only** (build, lint, unit tests). Merge if green.
  3. **Conflict:** return to the coder agent with the conflict context. After resolution, re-run **all gates including agent review** (conflict resolution is new code), then re-enqueue.
  4. **Red after rebase:** treated like a conflict - back to the coder.
- **FR-MQ-3** Order: engineer-defined dependency order, then FIFO.
- **FR-MQ-4** Prevention: when choosing parallel issues the scheduler warns if they likely touch the same packages.
- Before the queue (M5, D61): people merge change requests on the provider, and a watcher in `internal/mergequeue` moves the issue from `awaiting_human` to `merged` when it sees the merge. The queue keeps that watcher for merges made by hand.
- **Later optimization** (only if the queue becomes a bottleneck, unlikely at homelab scale): speculative batching - test A, A+B, A+B+C together, merge the longest green prefix.

## Releases and stages

A **Release** is a git SHA plus the set of issues it contains. It moves through an ordered list of **stages** defined per project. Staging, prod and rollout are not built-in concepts; they are one project's configuration.

```yaml
# web service
stages:
  - { name: preview, driver: flux-preview, verify: e2e-suite }
  - { name: staging, driver: flux,         verify: e2e-suite }
  - { name: prod,    driver: flux,         gate: human }
  - { name: rollout, driver: flagd }       # only if feature flags are enabled
```

```yaml
# CLI tool or library
stages:
  - { name: beta,   driver: github-releases, channel: beta,   verify: smoke-tests }
  - { name: stable, driver: github-releases, channel: stable, gate: human }
```

- **FR-RL-1** On merge, a **release agent** prepares the release: semver bump, changelog, release notes, interpretation of verification results. The control plane executes `Publish`, `Promote` and `Rollback` as Temporal Activities behind gates. Agents prepare; they do not deploy.
- **FR-RL-2** **Failure handling**: if a stage's verification fails, the release is marked failed, the stage is rolled back when its driver supports rollback (Flux: revert the env-repo commit), and a **bug issue** linked to the release and its suspect issues is created automatically.
- **FR-RL-3** Capability mismatches are rejected when stage config is saved.

### Release driver interface

```go
type ReleaseDriver interface {
    Capabilities() Capabilities // Rollback, Progressive, Health, Preview, ...
    Publish(ctx context.Context, r Release, s Stage) (Deployment, error)
    Status(ctx context.Context, d Deployment) (Status, error)
    Promote(ctx context.Context, d Deployment, step RolloutStep) error // only if Progressive
    Rollback(ctx context.Context, d Deployment) error                  // only if supported
}
```

- Drivers are **out-of-process plugins over gRPC** (hashicorp/go-plugin, like Terraform providers), so a new target never touches the control plane.
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
