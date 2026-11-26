# Issue page

One issue in full: its title, type and status, project, spec and acceptance criteria, what it is part of, waits for and is needed by, the actions that fit its status (start, cancel, complete, retry, approve or reject its refined spec), and its runs with a live view of the one in progress.

## Sub-features

- `issue-show` renders title, status chip, spec, acceptance criteria and relations.
- `issue-runs` lists the issue's runs and, for a live one, its steps with role, harness, model and cost.
- `issue-start` offers `Start` on a ready issue and shows the start panel.
- `issue-cancel` cancels with a required reason and records who and why.
- `issue-spec-approve` approves a refined spec waiting for approval, moving the issue to `ready`.
- `issue-spec-reject` rejects it with a required comment (the issue goes back to `draft`, held until someone asks again).
- `issue-complete` completes a parent (confirm dialog); not reachable in the seed.

## How to get to it (user POV)

- Click an issue key on the board, on the approvals inbox, on a release page or on the Runs list.
- Open `#/issues/<key>` (for example `#/issues/vfy-5`).

## Driving it with browser.mjs

Preconditions: fresh launch, name and token set. `vfy-5` is refining with a proposal; `vfy-3` is ready and waits for `vfy-2`; `vfy-6` has a pending human gate.

- **Show.** `$B goto '#/issues/vfy-6'`; `$B expect --text "Add a health endpoint"`; `$B expect --text "decide or answer in the approvals inbox"`; the steps list shows `implement succeeded` with `coder · claude-code · anthropic/claude-sonnet-5` and `approve awaiting human`.
- **Relations.** `$B goto '#/issues/vfy-3'`; `$B expect --text "waits for:"`; `$B expect --role link --name vfy-2`.
- **Approve a refined spec.** `$B goto '#/issues/vfy-5'`; `$B expect --text "The refined spec waits for approval below"`; `$B click --role button --name "Approve refined spec"`. Result: the page shows `spec approved by verifier` and status chip `ready`; `$S/frankie-verify sql "select status, spec_approved_by from issues where key='vfy-5'"` prints `ready|verifier`; `#/approvals` no longer lists a `vfy-5` spec card.
- **Reject a spec.** (fresh launch) On `#/issues/vfy-5`, `$B fill --placeholder "Comment (required to reject: what should change)" --value "split it"`, then `$B click --role button --name Reject`. The page shows `Held: split it (verifier)`, the chip reads `draft`; `sql "select status, spec_feedback from issues where key='vfy-5'"` prints `draft|split it (verifier)`.
- **Cancel.** `$B goto '#/issues/vfy-3'`; `$B click --role button --name "Cancel…"`; `$B fill --placeholder "Why is it canceled? (posted on GitHub)" --value "verification run"`; `$B click --role button --name "Cancel issue"`. Result: `Canceled by verifier on ...: verification run` and the chip reads `canceled`; `sql "select status, closed_by, close_reason from issues where key='vfy-3'"` prints `canceled|verifier|verification run`.
- **Start.** `$B goto '#/issues/vfy-2'`; `$B click --role button --name Start`; after `$B goto '#/issues/vfy-2'` again, `$B expect --text "Start requested by verifier"` passes. The `Start` button is gone once requested.
- **Proof.** Snapshot and screenshot before and after the action under `$EVIDENCE_DIR/issue-page/`, plus the `sql` view above.

## Gotchas

- `Cancel issue` is disabled until a reason is typed; the textarea has only a placeholder, no label.
- Cancel and complete tell the user that Frankie closes the GitHub issue within a minute. Intake is not running here, so `source_closed_at` stays null; that GitHub side effect is unverified.
- `Complete` appears only on a parent whose children are all finished; in the seed `vfy-1` has open children, so `issue-complete` is unreachable without first canceling `vfy-2`, `vfy-3` and `vfy-5` (cancel them, then complete `vfy-1` with `--accept-dialog`). Unverified.
- `Retry` needs a failed issue; the seed has none. `issue-retry` is unreachable and also needs Temporal to do anything.
- The live run view (events streaming) depends on the run transcript page loading; see `runs.md` for the Chromium 159 crash of `#/runs/<id>`. The issue page's own run summary renders fine.
