# Issue page

One issue in full: its title, type and status, project, spec and acceptance criteria, what it is part of, waits for and is needed by, the actions that fit its status (start, cancel, complete, retry, approve or reject its refined spec), and its runs with a live view of the one in progress.

## Sub-features

- `issue-show` renders title, status chip, spec, acceptance criteria and relations.
- `issue-runs` lists the issue's runs (linked by the last 8 characters of the run id) and follows the newest one, live or finished: its steps with role, harness, model and cost, and the agent's latest events.
- `issue-start` offers `Start` on a ready issue and shows the start panel.
- `issue-cancel` cancels with a required reason and records who and why; it is refused while a run is in progress.
- `issue-spec-approve` approves a refined spec waiting for approval, moving the issue to `ready`.
- `issue-spec-reject` rejects it with a required comment (the issue goes back to `draft`, held until someone asks again, with `Refine again` and `Approve spec as written`).
- `issue-complete` completes a parent whose sub-issues are all finished and not all canceled (confirm dialog); not reachable in the seed.

## How to get to it (user POV)

- Click an issue key on the board, on a parent's page or on a release page. On the Runs list and in the approvals inbox the key links to the run page instead.
- Open `#/issues/<key>` (for example `#/issues/vfy-5`).

## Driving it with browser.mjs

Preconditions: fresh launch, name and token set. `vfy-5` is refining with a proposal; `vfy-3` is ready and waits for `vfy-2`; `vfy-6` has a pending human gate.

- **Show.** `$B goto '#/issues/vfy-6'`; `$B expect --text "Add a health endpoint"`; `$B expect --text "decide or answer in the approvals inbox"`; the steps list shows `implement succeeded` with `coder · claude-code · anthropic/claude-sonnet-5` and `approve awaiting human`.
- **Live run.** `$B goto '#/issues/vfy-4'`; `$B expect --text "A run is in progress"`; `$B expect --text "Tool: Grep"`; `$B expect --role link --name "full transcript"` (that link leads to the run page, which crashes, see `runs.md`; the issue page's own live view works).
- **Relations.** `$B goto '#/issues/vfy-3'`; `$B expect --text "waits for:"`; `$B expect --role link --name vfy-2 --exact`. A parent shows its sub-issues instead: `$B goto '#/issues/vfy-1'`; `$B expect --text "contains 3 issues"`; `$B expect --role button --name "Start all under it"`.
- **Approve a refined spec.** `$B goto '#/issues/vfy-5'`; `$B expect --text "The refined spec waits for approval below"`; `$B click --role button --name "Approve refined spec"`. Result: the page shows `spec approved by verifier` and status chip `ready`; `$S/frankie-verify sql "select status, spec_approved_by from issues where key='vfy-5'"` prints `ready|verifier`; `#/approvals` no longer lists a `vfy-5` spec card.
- **Reject a spec.** (fresh launch) On `#/issues/vfy-5`, `$B fill --placeholder "Comment (required to reject: what should change)" --value "split it"`, then `$B click --role button --name Reject`. `Reject` is disabled until the comment is typed; click it with `--exact`. The page shows `Held: split it (verifier)`, the chip reads `draft`, and the buttons `Refine again` and `Approve spec as written` appear; `sql "select status, spec_feedback from issues where key='vfy-5'"` prints `draft|split it (verifier)`.
- **Cancel refused.** `$B goto '#/issues/vfy-6'`, `Cancel…`, fill the reason, `Cancel issue`: `$B expect --text "has a run in progress"`; vfy-6 stays `awaiting_human`. No change to the seed.
- **Cancel.** `$B goto '#/issues/vfy-3'`; `$B click --role button --name "Cancel…"`; `$B fill --placeholder "Why is it canceled? (posted on GitHub)" --value "verification run"`; `$B click --role button --name "Cancel issue"`. Result: `Canceled by verifier on ...: verification run` and the chip reads `canceled`; `sql "select status, closed_by, close_reason from issues where key='vfy-3'"` prints `canceled|verifier|verification run`.
- **Start.** `$B goto '#/issues/vfy-2'`; `$B click --role button --name Start --exact`; `$B expect --text "Start requested: vfy-2"` and `$B expect --text "Start requested by verifier"` (the page reloads itself). `$B expect --role button --name Start --exact --absent`: the button is gone once requested.
- **Proof.** Snapshot and screenshot before and after the action under `$EVIDENCE_DIR/issue-page/`, plus the `sql` view above.

## Gotchas

- `Cancel issue` is disabled until a reason is typed; the textarea has only a placeholder, no label.
- After a cancel or complete the page says `Closed: <key> (canceled). Frankie closes their GitHub issues within a minute.` Intake is not running here, so `source_closed_at` stays null; that GitHub side effect is unverified.
- `Complete` appears only on a parent whose sub-issues are all finished and at least one is not canceled (all canceled: cancel the parent instead). Only a merge makes a sub-issue `merged`, so `issue-complete` is unreachable here (`vfy-1`'s sub-issues can only be canceled). Unverified.
- `Retry` needs a failed issue; the seed has none, so `issue-retry` is unreachable. The retry itself only writes Postgres (`failed` to `ready` plus a start request); the run that would follow needs the scheduler and Temporal.
- The live run view on the issue page streams on its own and works; only its `full transcript` link and the runs-table links go to the run page, which crashes on Chromium 159 (`runs.md`).
