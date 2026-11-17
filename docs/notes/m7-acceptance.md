# M7 acceptance (2026-11-17)

M7's "done when" (`docs/spec/10-roadmap.md`) was checked live. The factory runs on Scaleway Kapsule (`admin@frankie-k8s-v2`), deployed by Flux from `misarescu/frankie-cd`. Goodreads-Clone's staging and previews run on the homelab, deployed by its Flux from `misarescu/Goodreads-CD`, and Frankie reaches them over Tailscale.

| Criterion | Result |
|---|---|
| A merged Goodreads-Clone issue becomes a release that deploys to staging on the homelab and passes the e2e suite there | Met: gc-74 released as `910ce65` (177 tests), and every queue merge since |
| A deliberately broken release is rolled back by reverting its commit and produces a bug issue linking release and suspect issue | Met: `b6f703d` (gc-74's tests) and `dc515ab` (gc-78's tests, with the release agent's analysis) |
| A Goodreads-Clone change request's e2e tests run in its preview before it merges | Met: gc-77's change request #153 passed 280 tests in its preview, which was then removed, before the queue merged it |

The release agent (D129) and the releases page (D130) were added after the criteria were met, and were accepted the same way.

## Releases on staging

Every release Goodreads-Clone got in M7, in order. Times are UTC on 2026-11-17.

| Commit | Issues | Outcome | Notes |
|---|---|---|---|
| `0ddfbc9` | gc-73 | succeeded 11:04 | First release by the `flux` driver (D117), before stages verified |
| `8121a6e` | none | succeeded 11:18 | Should have contained gc-71: the merge queue asked for the release before it moved the issue to `merged`. Fixed by D121 |
| `eadd480` | gc-71 | succeeded 11:35 | |
| `910ce65` | gc-72, gc-74 | succeeded 12:44 | **First verified release**: `verify: e2e-suite`, 177 tests passed |
| `e89c833` | gc-75 | succeeded 14:23 | The first `build-e2e` issue; 219 tests |
| `b6f703d` | none | rolled back 14:37 | Deliberately broken; 4 of 219 tests failed |
| `d48a6d6` | gc-77 | succeeded 18:56 | Checked in its preview before the merge |
| `abb5e70` | gc-78 | succeeded 21:13 | **First version**: `v0.1.0`, a minor bump with the agent's notes, published as a GitHub Release |
| `dc515ab` | none | rolled back 21:18 | Deliberately broken; 7 of 369 tests failed; the bug issue has the release agent's analysis |

Each release commits the image tag to `apps/goodreads/staging/kustomization.yaml` in Goodreads-CD. It then waits until `/api/version` reports the commit, and runs the Bruno suite against staging from a pod on Kapsule. A rollback commits the previous tag back.

## The broken releases

Both were made the same way:
- a draft pull request whose image the Goodreads-Clone CI publishes;
- a release asked for by hand, with the `release-request` signal and the draft's branch as `BaseBranch`.

**`b6f703d`** (Goodreads-Clone#149): a book's review count became the length of its star spread.
- Four gc-74 tests failed on staging.
- Staging was rolled back to `e89c833`.
- Bug #150 (gc-76) named the release, the stage, the rollback and the failing tests with their issue, gc-74.

**`dc515ab`** (Goodreads-Clone#157): a reader's tags came back alphabetically instead of by book count.
- Seven gc-78 tests failed on staging, and staging was rolled back to `abb5e70`.
- The release contained no issue Frankie merged, so the suspects by test tag were empty.
- The release agent's analysis ($0.023) still named gc-78 as the likely culprit, with high likelihood. It cited the exact change (`ORDER BY books DESC, t.tag` became `ORDER BY t.tag`, in the query and in the generated code), explained why each of the seven tests failed, and gave the fix.
- The analysis is a section of bug #158.

Both test bugs were closed as not planned, and their draft pull requests were closed.

## Issues through `build-e2e`

`build-e2e` (D122) runs the e2e test writer at the same time as the coder, each on its own branch. It merges the two branches, then runs `checks` and `mise run e2e:local` before the review. Since D126, the change request's head is also checked in a preview before it is enqueued.

| Issue | Ran | Result | Cost |
|---|---|---|---|
| gc-75 tags list (Goodreads-Clone#147) | 13:57-14:21 | One pass, merged `e89c833`, released | $0.56 |
| gc-77 similar books (#152) | 17:23, then retried 18:16-18:57 | First run failed (below); the retry merged `d48a6d6`, released | $1.14, both runs |
| gc-78 a reader's tags (#155) | 20:33-21:13 | Merged `abb5e70`, released as `v0.1.0` | $0.75 |

- **gc-75:** the writer added 19 tagged requests. The coder also added three untagged requests of its own, which D123 then forbade.
- **gc-77's first run** found a dead end. The writer's suite missed a setup request, so the `e2e` gate failed. The coder rightly changed nothing, which failed the run as "no changes". D128 now lets a coder that a failed gate sent back dispute the failure: in `build-e2e` its report goes to the test writer.
- **gc-77's retry:**
  - `e2e` failed again;
  - the coder disputed, and the writer added the missing request;
  - the gates and the review passed;
  - in the preview, `preview-ci` took 2 minutes and `preview` 3 minutes. Goodreads-CD got commit `0790f6e` (add) and `a70c109` (remove), and 280 tests passed at `/pr-153/`;
  - then the queue merged it and staging verified it.
- **gc-78** took the same path. The first `e2e` failed, the coder disputed (its second attempt pushed nothing), the writer fixed its tests, and the rest passed on the first try.
- **Release agent on gc-78's release:**
  - The `notes` step ran while staging deployed ($0.032). It proposed a minor bump, which is right for a new endpoint, and wrote one accurate "Added" entry for gc-78.
  - The control plane computed `v0.1.0`, the repo's first version. It published a GitHub Release at `abb5e70` once staging passed.

## Found and fixed during M7

- **Kapsule's pod CIDR was inside Tailscale's range.** `100.64.0.0/15` sits in Tailscale's CGNAT range, so tailscaled dropped forwarded pod traffic. The cluster was recreated with `10.48.0.0/15` (D119), and Postgres was moved by dump and restore.
- **A release linked the previous merge's issue** (`8121a6e` above): the queue moved the issue to `merged` only after it had asked for the release (D121).
- **A coder's CI failure was invisible to it:** the queue now gives the coder the tail of a failed check's log (D120).
- **A wrong e2e test was a dead end** (gc-77), fixed by D128.
- **Verification results lived only in Temporal's history.** Since D130 each deployment records its verification, and the releases page shows it.

## Findings for later

- **Flux is slow to pick up a frankie-cd merge.** Each deploy annotated the GitRepository and the Kustomizations with `reconcile.fluxcd.io/requestedAt`; there is no `flux` CLI in the toolchain.
- **Old agent branches stay in Goodreads-Clone.** Branches of merged change requests are never deleted (dozens of `agent/gc-*`).
- **Releases before D130 show no verification** on the releases page. Their results are only in Temporal's history.
- **Paths tested only in unit and replay tests,** not live:
  - a dispute that ends at the escalation gate (outside a writer group, or both sides standing by their work);
  - a provider error while publishing a version;
  - a failed release agent session falling back to the bump from issue types and notes listing the issues;
  - a preview with no room (`drivers.ErrNoRoom`).
- **Frankie's own canary in a preview** is M7.1.
