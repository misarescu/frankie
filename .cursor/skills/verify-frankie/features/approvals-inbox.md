# Approvals inbox

Everything waiting for a person, across all projects, in one list at `/approvals`: questions agents asked with the ask-human tool, human gates waiting for a decision (approve, request changes, reject; on issues and before release stages), and refined specs waiting for approval before their issue runs. A segmented filter (`All`, `Gates`, `Questions`, `Specs`, each with its count once loaded: the radio is named `Gates 2`) sets `?kind=gate|question|spec`; each item is decided in place and leaves the list once its write lands.

## Sub-features

- `inbox-empty` says `Nothing waits for you` (or, per kind, `No gates wait for a decision`, `No agent waits for an answer`, `No refined spec waits for approval`).
- `inbox-gate` shows a pending human gate: kind (`Protected paths` or `Human gate`), waiting time, expiry, the issue with its title, `Run · <step>`, `Review the change request`, commit, reason, comment box and the decision buttons that gate allows.
- `inbox-release-gate` shows a gate before a release stage (D139): `Release gate`, `Deploy <version or sha7> to <stage>`, a link `Release <sha7> of <repo>` to its release page (plain text until the runs and releases have answered), and only `Approve` and `Reject`.
- `inbox-spec` shows a refined spec (summary, refined spec, acceptance criteria, files it touches, `Original text` collapsed, `Refinement run` when the issue has a run, not in the seed) with `Approve spec`, `Send back with feedback` (needs feedback) and `Open <key>`.
- `inbox-question` shows an agent's question (`The <step> step of <key> asks`, `the agent is paused until you answer`) with `Your answer` and `Answer`, disabled until text is typed.
- `inbox-decide-spec` approving or sending back a spec takes effect at once.
- `inbox-decide-gate` approving, requesting changes or rejecting a gate signals the run's workflow.

## How to get to it (user POV)

- `Approvals` in the sidebar (its badge counts everything waiting), the command palette, the board's `Waiting for you` and `Waits for` links, or an issue banner's `Open in Approvals`.
- From a release page whose production gate waits: the same gate is decided there in place.

## Driving it with browser.mjs

Preconditions: fresh launch, name and token set (footer menu `Your name and API token`). The seed has a gate on `vfy-6` (reason `Protected paths changed: .golangci.yml`, `Request changes` allowed), a gate before release `fedcba9`'s `production` stage, a question on `vfy-12` and a spec on `vfy-5`.

- **Counts and question.** `$B goto /approvals`; `$B expect --role radio --name "All 4"`; `$B expect --role radio --name "Gates 2"`; `$B expect --in-section vfy-12 --text "Which sender address should confirmation emails use"`; `$B expect --in-section vfy-12 --role button --name Answer --disabled`.
- **Gate card.** `$B expect --in-section vfy-6 --text "Protected paths changed in vfy-6"`; `$B expect --in-section vfy-6 --role button --name Approve --exact`; `$B expect --in-section vfy-6 --role button --name "Reject and close"`; `$B expect --in-section vfy-6 --role button --name "Request changes" --disabled`; `$B fill --in-section vfy-6 --label Comment --value "please revert"`; `$B expect --in-section vfy-6 --role button --name "Request changes" --enabled`; `$B click --in-section vfy-6 --role button --name "Request changes"`; `$B expect --in-section vfy-6 --text "workflows are not running"`.
- **Release gate card.** Scope it by its title: `$B expect --in-section "Deploy fedcba9" --role link --name "Release fedcba9 of verify/demo"`; `--role button --name Approve --exact`; `--role button --name Reject --exact`; `--role button --name "Request changes" --absent`. The link opens `/releases/…0602`.
- **Filter.** `$B click --role radio --name Gates` goes to `/approvals?kind=gate` (a history entry).
- **Approve a spec.** `$B expect --in-section vfy-5 --text "Approve the refined spec of vfy-5"`; `$B expect --in-section vfy-5 --role button --name "Send back with feedback" --disabled`; `$B click --in-section vfy-5 --role button --name "Approve spec"`. Result: toast `Approved the spec of vfy-5.`, the card leaves (`$B expect --text "Approve the refined spec of vfy-5" --absent`); `sql "select status, spec_approved_by from issues where key='vfy-5'"` prints `ready|verifier`; `curl -s $API/api/specs` is `[]`; `/approvals?kind=spec` shows `No refined spec waits for approval`.
- **Decide a gate or answer (expected locally).** `Request changes`, `Approve`, `Reject` and `Answer` show `workflows are not running` inside the card and leave the item pending. That is the boundary of this stand-in, not a pass of `inbox-decide-gate`.
- **Without a token.** A write opens the `API token` dialog (`The API needs a valid token for that.`).
- **Proof.** Screenshots before and after the spec approval under `$EVIDENCE_DIR/approvals-inbox/`, plus the `sql` and `curl` views.

## Gotchas

- Cards are `<section>`s named by their key; `--in-section` matches visible text, so scope a release gate by `Deploy <sha7>`, not by `release-<sha7>`. `Approve` also prefix-matches `Approve spec`: use `--exact` for gates.
- Gate decisions and answers go through Temporal signals; locally they fail with `workflows are not running`. Only spec decisions work here.
- The inbox polls every 5 s; a decided item disappears when the write's refresh lands. Filter names carry the count once loaded (`Gates 2`): match them without `--exact`.
- A cold load shows the filter without counts and a loading list until approvals, questions and specs have all answered; one failing shows `Could not load the <kind>` with `Try again` above what loaded.
- A release gate's card links `Release <sha7> of <repo>` to its release once the runs or the releases answer; until then the title is muted with `finding the release` after it (`role=status`).
- `Review the change request` links to `https://example.invalid/...` in the seed; do not follow it.
- The empty inbox for gates and questions needs a workflow to resolve them; it is shown by fulfilling `/api/approvals`, `/api/questions` and `/api/specs` with `[]` in Playwright.
