# Releases

The releases page lists a project's releases newest first with version, bump, status, how far each got through its stages and the issues it contains. A release page shows each stage's outcome and verification, the release notes and who wrote them, the issues, and the release agent's runs.

## Sub-features

- `releases-list` lists releases with version, short commit, bump, status chip, stage chips and issue links.
- `release-stages` shows each stage that has started with driver, status, time, reason and its verification summary. A stage waiting at its human gate (D139) has not started, so it is missing here and from the list's stage chips; its gate is in the approvals inbox.
- `release-notes` shows the notes and whether the agent or the fallback wrote them.
- `release-issues` lists the release's issues with status.
- `release-agent-runs` lists the release's runs (the release agent's sessions and stage gates) with their steps; the section is absent when there are none.
- `releases-empty` explains that a merge from the merge queue asks for a release when the project declares stages.

## How to get to it (user POV)

- Open `#/releases` (the `Releases` link in the Runs, Issues and Approvals headers, or the release page's breadcrumb).
- Click a release name on the list, or open `#/releases/<release id>`.
- From a release run row on the Runs page (`release <version>`), the `release` link.

## Driving it with browser.mjs

Preconditions: fresh launch. The seed has release `v0.1.0` (id `00000000-0000-7000-8000-000000000601`, commit `1234567`, minor bump, succeeded) with a succeeded `staging` stage (verification `12 of 12 requests passed`) and a pending `production` stage, containing `vfy-7`; and release `fedcba9` (id `...0602`, patch bump, in progress, no version yet) whose `staging` passed and whose `production` stage waits at its human gate.

- **List.** `$B goto '#/releases'`; `$B expect --role heading --name Releases`; `$B expect --in-row v0.1.0 --text succeeded`; `$B expect --in-row v0.1.0 --text staging`; `$B expect --in-row v0.1.0 --role link --name vfy-7`.
- **Open it.** `$B click --role link --name v0.1.0`; `$B expect --text "verify/demo@1234567"`; `$B expect --text "12 of 12 requests passed"`; `$B expect --text "written by the release agent"`; `$B expect --role link --name vfy-7`.
- **Stages.** Release page has `staging · flux succeeded` and `production · flux pending`; `$B expect --role heading --name "Release agent" --absent` (no runs).
- **Gated release.** `$B goto '#/releases'`; `$B expect --in-row fedcba9 --text "in progress"`; `$B click --role link --name fedcba9 --nth 0`; `$B expect --text "staging · flux succeeded"`; `$B expect --text "production · flux" --absent`; `$B expect --role heading --name "Release agent"`; `$B expect --in-row production --text running` (the gate step of the release run).
- **Navigate to the issue.** `$B click --role link --name vfy-7` lands on `#/issues/vfy-7`.
- **Missing release.** `$B goto '#/releases/00000000-0000-7000-8000-000000000699'`; `$B expect --text "no such release"`.
- **Proof.** Snapshot and screenshot of the release page under `$EVIDENCE_DIR/releases/` and `curl -s $API/api/releases/00000000-0000-7000-8000-000000000601`. The list endpoint needs the project: `curl -s "$API/api/releases?project=verify/demo"` (400 without it).

## Gotchas

- Everything here is read-only; deployment, verification and the release agent are driven by Temporal and the Flux driver on the real factory and are unverified by this stand-in. Approving or rejecting the `fedcba9` gate answers `workflows are not running` (see `approvals-inbox.md`). The seeded `v0.1.0` (succeeded with `production` pending) cannot happen in a real run; it is a render fixture.
- `releases-empty` is unreachable with this seed (releases exist), and the skill does not remove seed rows. A project that does not exist gives `no such project`, not the empty state.
- A finished release's page does not poll; an in-progress one refreshes every 5 s.
- The list filters by the project in `localStorage` (`frankie.project`); with one project the name is shown as plain text, not a select.
- `Verification` failures (a failed stage with a bug issue link) are not seeded; `release-failed` is not covered.
