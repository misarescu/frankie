# M6 acceptance (2026-11-15)

M6's "done when" (`docs/spec/10-roadmap.md`), checked on the homelab, concrete profile, `scheduler.maxConcurrentIssues=2`, images `sha-8977326` (helm revision 47):

| Criterion | Result |
|---|---|
| Two issues editing overlapping code both reach `main`, the second sent back to the coder on conflict and re-reviewed | Met: gc-41 merged first; gc-40 conflicted in the queue, went back to its coder with both sides, passed the gate and review again, and merged |
| `main` is never red | Met: each change request merged only after the queue brought it up to date, re-ran its gates on that commit and saw Goodreads-Clone's own CI pass; each squash commit is the tree that was checked |
| A change request is approved and merged from Frankie | Met: gc-42 (`needs-merge-approval`) waited in the inbox, was approved, and merged through the queue |
| A Goodreads-Clone bug runs the bug-fix workflow from investigation to a merged fix with its reproducing test | Met: gc-43 ran every step of `bug-fix` and merged with its test |

## The demo epic: gc-39 "Reading challenges and stats" (Goodreads-Clone#80)

Two tasks that edit the same files (`ChallengeRepository`, the routes, `challenges.sql`), a task that needs merge approval, and a real bug. The epic was started from Frankie at 17:51 UTC; the last merge was at 18:26. In all it cost $1.11.

| Issue | Workflow | Ran | Change request | Merged | Cost |
|---|---|---|---|---|---|
| gc-41 list a user's challenges (#77) | `default-build` | 17:51-18:03 | #81, opened 18:01:46 | 18:03:37, `e08fdab` | $0.26 |
| gc-40 delete a challenge (#76) | `default-build` | 17:51-18:12 | #82, opened 18:01:46 | 18:12:27, `3a79866` | $0.40 |
| gc-42 protect the default shelves (#78) | `default-build`, merge approval | 18:03-18:20 | #83, opened 18:10:56 | 18:20:51, `410744c` | $0.20 |
| gc-43 a finished book counted twice (#79, bug) | `bug-fix` | 18:12-18:26 | #84, opened 18:23:41 | 18:26:24, `2d5f4bb` | $0.25 |

- **Queue and CI:** gc-40 and gc-41 opened their change requests in the same second. gc-41 entered the queue first; it was already up to date, so the queue waited for Goodreads-Clone's CI (lint and tests, about 1.5 minutes) and merged it.
- **Conflict:** gc-40's branch update then conflicted with gc-41's merge. The queue sent it back to `implement` (attempt 2) with both sides of the conflicting files. The coder kept both changes; `checks` and the review ran again; the queue updated, re-gated and merged it on its fourth queue attempt.
- **Merge approval:** gc-42 carried `needs-merge-approval`, so after its change request opened it waited in the inbox (issue `awaiting_human`). Once approved, the queue merged `main` into its branch, re-ran its command gates on the new commit (`merge-checks`) and waited for CI before merging.
- **Bug fix:** gc-43 (Goodreads-Clone#79) ran `bug-fix`.
  - `investigate` (high confidence) traced the double count to `GetUserReadingStats` summing `shelf_books` rows where `books_on_shelves` counts distinct books. It also found an existing test that encoded the bug.
  - `testable` was true, so `decide` was skipped.
  - `reproduce` corrected the old test's expectation and added a test of its own. `shows-bug` saw the suite fail.
  - `fix` changed the query. `keeps-test` confirmed the reproducing test was untouched; `checks` passed; `bug-reviewer` passed.
  - The change request carried the findings; the queue updated, re-gated and merged it.
- **History:** Goodreads-Clone's `main` gained four squash commits, one per issue, each titled with its key. Frankie closed the four issues and the epic on GitHub as completed.
- **Overlap:** the scheduler warned that gc-40 and gc-41 overlap (D63). The queue serialized them, and the conflict loop resolved the rest.

## Registry garbage collection (D84)

The first scheduled collection after deploying #97 (13:59) deleted 16 manifests and forgot 2 records. The registry's init container then collected the blobs: 7.3 GiB down to 4.4 GiB. Afterwards the records and the registry's tags matched, and the `toolchain` e2e passed. It runs every 24 hours when no build runs, keeping the three newest images per repository and any built within a day.

## Found and fixed during M6

- **Fine-grained tokens cannot read check runs.** The `frankie-github` token has no Checks permission, so `CommitStatus` falls back to the Actions workflow runs of the commit (D86). The token needs Actions: read and Commit statuses: read.
- **The `preserve` gate kept whole files.** The fix could not touch any file the reproducer changed; gc-43 got through only because the reproducer itself corrected the old expectation in the shared test file. The gate now keeps the blocks of lines the step added and quotes any it lost (D97, #110).
- **Skipped steps looked unfinished.** gc-43's `decide` showed as not started while the run went past it. The run card now shows a conditional step the run went past as skipped, with its condition (D98, #111).
- **Helm `--reuse-values` ignores new chart defaults.** Revision 44 deployed with registry collection off. Every deploy now sets the values merged PRs added since the last one.

## Findings for later

- **Queued issues hold scheduler slots.** An issue waiting in the merge queue (for CI, an update or its gates) still counts against `maxConcurrentIssues`, so gc-42 started only when gc-41 merged. With Goodreads-Clone's 1.5-minute CI this cost little; Frankie's own CI takes about 6 minutes. The queue still runs gate pods, which is the memory the limit protects (open question 19).
- **`expect: fail` passes on any failure,** including a test that does not compile. The reproducer's prompt and the bug review guard against it, and gc-43's test failed on the wrong count, but the gate cannot tell which test is the new one.
- **Paths tested only in unit and replay tests,** not live:
  - request changes on a merge approval;
  - an untestable bug (the `decide` gate and its send-back);
  - Reject (closing the change request);
  - the `rebase` merge method;
  - a red CI run sent back to the coder;
  - the queue's `busy` and `refused` outcomes.
- **Goodreads-Clone's CI runs only on pull requests,** so its `main` has no CI run of its own. The queue's guarantee rests on merging exactly the commit that was checked, with the base unchanged since the update.
- **Control plane start:** the new pod's first Postgres connection was refused once (its NetworkPolicy was not yet in place) and succeeded on retry.
- **Egress bootstrap** (open question 18) is still open.
