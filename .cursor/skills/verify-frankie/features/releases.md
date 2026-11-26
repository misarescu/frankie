# Releases

The releases page lists a project's releases newest first with version, bump, status, how far each got through its stages and the issues it contains. A release page shows each stage's outcome and verification, the release notes and who wrote them, the issues, and the release agent's runs.

## Sub-features

- `releases-list` lists releases with version, short commit, bump, status chip, stage chips and issue links.
- `release-stages` shows each stage with driver, status, time, reason and its verification summary.
- `release-notes` shows the notes and whether the agent or the fallback wrote them.
- `release-issues` lists the release's issues with status.
- `releases-empty` explains that a merge from the merge queue asks for a release when the project declares stages.

## How to get to it (user POV)

- Open `#/releases` (the `Releases` link in every header).
- Click a release name on the list, or open `#/releases/<release id>`.
- From a release run row on the Runs page (`release <version>`), the `release` link.

## Driving it with browser.mjs

Preconditions: fresh launch. The seed has release `v0.1.0` (id `00000000-0000-7000-8000-000000000601`, commit `1234567`, minor bump, succeeded) with a succeeded `staging` stage (verification `12 of 12 requests passed`) and a pending `production` stage, containing `vfy-7`.

- **List.** `$B goto '#/releases'`; `$B expect --role heading --name Releases`; `$B expect --in-row v0.1.0 --text succeeded`; `$B expect --in-row v0.1.0 --text staging`; `$B expect --in-row v0.1.0 --role link --name vfy-7`.
- **Open it.** `$B click --role link --name v0.1.0`; `$B expect --text "verify/demo@1234567"`; `$B expect --text "12 of 12 requests passed"`; `$B expect --text "written by the release agent"`; `$B expect --role link --name vfy-7`.
- **Stages.** Release page has `staging · flux succeeded` and `production · flux pending`.
- **Navigate to the issue.** `$B click --role link --name vfy-7` lands on `#/issues/vfy-7`.
- **Proof.** Snapshot and screenshot of the release page under `$EVIDENCE_DIR/releases/` and `curl -s $API/api/releases/00000000-0000-7000-8000-000000000601`.

## Gotchas

- Everything here is read-only; deployment, verification and the release agent are driven by Temporal and the Flux driver on the real factory and are unverified by this stand-in. The `Release agent` runs table is empty in the seed.
- `releases-empty` is unreachable with this seed (a release exists). Verify it by querying a project with no releases or after removing the seed row, which this skill does not do.
- The list filters by the project in `localStorage` (`frankie.project`); with one project the name is shown as plain text, not a select.
- `Verification` failures (a failed stage with a bug issue link) are not seeded; `release-failed` is not covered.
