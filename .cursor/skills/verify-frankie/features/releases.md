# Releases

`/projects/<prefix>/releases` lists a project's releases newest first: version or short commit, bump, status, the stages as a pipeline (a stage waiting at its human gate shows `waiting for you`), the issues it contains and when it was created and finished; at phone width each release is a card. `/releases/<id>` shows one release: header with name, repository, commit, bump and status; the production gate decided in place while it waits (D139); the pipeline; each stage's deployment with driver, status, times, reason, verification and failed tests; release notes and who wrote them; details (version text, provider release, commit, project, times); the issues; the bug issue a failed verification opened; and the release agent's runs with steps and cost.

## Sub-features

- `releases-list` table (cards at phone width): name link, short commit, bump, status chip, pipeline, issue count with links, created, finished.
- `release-gate` a waiting stage gate as `<Stage> waits for you` with the reason, expiry, comment box, `Approve` and `Reject`.
- `release-stages` each deployment: stage, driver, status, started and finished, reason, `verification succeeded|failed|inconclusive` with its summary (`no verification` for a stage without a verify step; not in the seed), and the failed tests with their issue links and messages.
- `release-notes` the notes and `written by the release agent` (or the fallback text); `Not written yet.` while it is live, `None.` once finished without notes.
- `release-issues` the issues with their status.
- `release-bug` the reason and `Bug issue <key> <title>` with a link `on the provider`.
- `release-agent-runs` `Run of <time>` links to `/runs/<id>`, its steps and cost; the card is absent when there are none.
- `releases-empty` `No releases yet` for a project without releases (`/projects/oth/releases`).
- `release-missing` `No such release` inside the page for an unknown or malformed id, with a button to the current project's releases.

## How to get to it (user POV)

- `Releases` in the sidebar, or the release page's breadcrumb (project > Releases > release).
- A row of the list (the whole row opens it), or `/releases/<release id>`.
- From the approvals inbox, a release gate's `Release <sha7> of <repo>` link.

## Driving it with browser.mjs

Preconditions: fresh launch. Seeded in `verify/demo`: `c0ffeec` (in progress, staging deploying, agent running), `fedcba9` (in progress, staging passed, production gate waiting, release run `…0208`), `b0b0b0b` (rolled back, 12 of 14 with two failed tests, bug issue vfy-13, agent cost $0.19), `v0.1.0` (succeeded, production pending).

- **List.** `$B goto /projects/vfy/releases`; `$B expect --in-row v0.1.0 --text succeeded`; `$B expect --in-row fedcba9 --text "waiting for you"`; `$B expect --in-row v0.1.0 --role link --name vfy-7`.
- **Gated release.** `$B click --role link --name fedcba9`; `$B expect --role heading --name "Production waits for you"`; `$B expect --in-section "Production waits for you" --text "The gate expires in"`; `$B expect --in-section "Production waits for you" --role button --name "Request changes" --absent`; `$B expect --text "Not written yet."`; `$B expect --role link --name "Run of"`; `$B click --in-section "Production waits for you" --role button --name Reject --exact` shows `workflows are not running` (unverified locally).
- **Rolled back.** `$B goto /releases/00000000-0000-7000-8000-000000000603`; `$B expect --text "verification failed" --exact`; `$B expect --role list --name "Failed tests"`; `$B expect --text "profile/upload avatar png"`; `$B expect --role link --name vfy-13` (the bug issue); `$B expect --role link --name "on the provider"`; `$B expect --text "no version: its issues are in the next release"`; `$B expect --text "written by the release agent"`; `$B expect --text "Release agent" --exact` (the card); `$B expect --text '$0.19'`. A plain `--text "12 of 14"` or `--text "Release agent"` lands first on the reason line or the header, not on what it names.
- **Missing release.** `$B goto /releases/00000000-0000-7000-8000-000000000699`; `$B expect --text "No such release"`; the same for `/releases/not-a-uuid`. Empty: `$B goto /projects/oth/releases`; `$B expect --text "No releases yet"`.
- **Proof.** Screenshots under `$EVIDENCE_DIR/releases/` and `curl -s $API/api/releases/<id>`. The list endpoint needs the project: `curl -s "$API/api/releases?project=verify/demo"` (400 without it).

## Gotchas

- Everything but the gate is read-only; deployment, verification and the release agent are driven by Temporal and the Flux driver on the real factory. The seeded `v0.1.0` (succeeded with `production` pending) cannot happen in a real run; it is a render fixture.
- A stage waiting at its gate has no deployment row; the pipeline adds it from the pending approval of one of the release's runs.
- The list refreshes every 10 s and its gates every 5 s; a release page every 3 s while live, else 15 s.
- On a cold load a gated stage shows `waiting for you` and the gate banner only once `/api/approvals` (and on the list `/api/runs`) answer, and the bug issue line shows its raw source link until the project's issues answer: wait with `expect` before a screenshot.
