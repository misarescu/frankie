# Approvals inbox

Everything waiting for a person, in one list: questions agents asked with the ask-human tool, human gates waiting for a decision (approve, request changes, reject), and refined specs waiting for approval before their issue runs.

## Sub-features

- `inbox-empty` says `Nothing waits for an answer or a decision.` when nothing waits.
- `inbox-gate` shows a pending human gate: issue, step, reason, commit, expiry, comment box and the decision buttons that gate allows.
- `inbox-release-gate` shows a gate before a release stage (D139): `release-<sha7>`, `Release <sha7> of <repo>`, the stage as the step, and only `Approve` and `Reject`.
- `inbox-spec` shows a refined spec (summary, spec, acceptance criteria, files it touches, original issue) with `Approve spec` and `Reject`.
- `inbox-question` shows an agent's question with an answer box (none in the seed).
- `inbox-decide-spec` approving or rejecting a spec takes effect at once.
- `inbox-decide-gate` approving, requesting changes or rejecting a gate signals the run's workflow.

## How to get to it (user POV)

- Open `#/approvals`, or follow `Approvals` in the header of the Runs, Issues and Releases pages.
- From an issue page that says its run waits for a person ("decide or answer in the approvals inbox").

## Driving it with browser.mjs

Preconditions: fresh launch, name and token set. The seed has a gate on `vfy-6` (`approve`, reason `Protected paths changed: .golangci.yml`, `Request changes` allowed), a gate before release `fedcba9`'s `production` stage, and one spec on `vfy-5`.

- **Gate card.** `$B goto '#/approvals'`; `$B expect --in-section vfy-6 --text "Protected paths changed: .golangci.yml"`; `$B expect --in-section vfy-6 --role button --name Approve --exact`; `$B expect --in-section vfy-6 --role button --name "Reject and close"`; `$B expect --in-section vfy-6 --role button --name "Request changes" --disabled` (enabled once a comment is typed).
- **Release gate card.** `$B expect --in-section release-fedcba9 --text "Release fedcba9 of verify/demo"`; `$B expect --in-section release-fedcba9 --text "passed the stages before production"`; `$B expect --in-section release-fedcba9 --role button --name Approve --exact`; `$B expect --in-section release-fedcba9 --role button --name Reject --exact`; `$B expect --in-section release-fedcba9 --role button --name "Request changes" --absent`.
- **Spec card.** `$B expect --in-section vfy-5 --text "Add a tags array to a todo"`; `$B expect --in-section vfy-5 --role button --name "Approve spec"`; `$B expect --in-section vfy-5 --role button --name Reject --disabled` (a comment is required).
- **Approve a spec.** `$B click --in-section vfy-5 --role button --name "Approve spec"`. Result: the `vfy-5` card disappears (`$B expect --text "spec" --in-section vfy-5 --absent`); `sql "select status from issues where key='vfy-5'"` prints `ready`; `curl -s $API/api/specs` is `[]`.
- **Empty state.** After `vfy-5` is approved the gate cards still show; the empty message needs both gates resolved, which needs a workflow. Unreachable here.
- **Decide a gate (expected locally).** `$B click --role button --name Approve --in-section vfy-6 --exact` shows `workflows are not running` in the card and leaves the approval `pending` (`sql "select status from approvals"`). The release gate answers the same way (`--in-section release-fedcba9`). That is the boundary of this stand-in, not a pass of `inbox-decide-gate`: report it as unverified.
- **Proof.** Screenshot and snapshot of `#/approvals` before and after the spec approval under `$EVIDENCE_DIR/approvals-inbox/`, plus the `sql` and `curl` views.

## Gotchas

- Cards are `<section>`s without ARIA labels; scope buttons with `--in-section <issue key>`. `Approve` also prefix-matches `Approve spec`: use `--exact` for gates.
- Decisions on gates and answers to questions go through Temporal signals; locally they fail with `workflows are not running`. Only spec decisions work here.
- The inbox polls every 10 s; a decided gate disappears about 1.5 s after the decision (the page waits before reloading).
- `Review the change` links to `https://example.invalid/...` in the seed; do not follow it.
- The API token is required: without it the card shows `this needs the API token; set it at the top of the page` and the token bar opens with `The API needs a valid token for that.` Without a name a spec decision still succeeds but is recorded as `web` (seen 2026-11-28: `spec_approved_by` was `web`).
- A release gate's key `release-<sha7>` links to its release run's page, which crashes on Chromium 159 (`runs.md`). The seeded one reads `expires in 3 days`.
