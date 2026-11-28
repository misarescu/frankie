# Releases

`/projects/<prefix>/releases` lists a project's releases newest first: version or short commit, bump, status, the stages as a pipeline (a stage waiting at its human gate shows `waiting for you`), the issues it contains and when it was created and finished; at phone width each release is a card. `/releases/<id>` shows one release: header with name, repository, commit, bump and status; the production gate decided in place while it waits (D139); the pipeline; each stage's deployment with driver, status, times, reason, verification and failed tests; release notes and who wrote them; details (version text, provider release, commit, project, times); the issues; the bug issue a failed verification opened; and the release agent's runs with steps and cost.

## Sub-features

- `releases-list` table (cards at phone width): name link, short commit, bump, status chip, pipeline, issue count with links, created, finished.
- `release-gate` a waiting stage gate as `<Stage> waits for you` with the reason, expiry, comment box, `Approve` and `Reject`.
- `release-stages` each deployment: stage, driver, status, started and finished, reason, `verification succeeded|failed|inconclusive` with its summary, and the failed tests with their issue links and messages.
- `release-notes` the notes and `written by the release agent` (or the fallback text); `Not written yet.` while it is live.
- `release-issues` the issues with their status.
- `release-bug` the reason and `Bug issue <key> <title>` with a link `on the provider`.
- `release-agent-runs` `Run of <time>` links to `/runs/<id>`, its steps and cost; the card is absent when there are none.
- `releases-empty` `No releases yet` for a project without releases (`/projects/oth/releases`).
- `release-missing` `No such release` inside the page for an unknown or malformed id.

## How to get to it (user POV)

- `Releases` in the sidebar, or the release page's breadcrumb (project > Releases > release).
- A row of the list (the whole row opens it), or `/releases/<release id>`.
- From the approvals inbox, a release gate's `Release <sha7> of <repo>` link.

## Driving it with browser.mjs

Preconditions: fresh launch. Seeded in `verify/demo`: `c0ffeec` (in progress, staging deploying, agent running), `fedcba9` (in progress, staging passed, production gate waiting), `b0b0b0b` (rolled back, 12 of 14 with two failed tests, bug issue vfy-13, agent cost $0.19), `v0.1.0` (succeeded, production pending).

- **List.** `$B goto /projects/vfy/releases`; `$B expect --in-row v0.1.0 --text succeeded`; `$B expect --in-row fedcba9 --text "waiting for you"`; `$B expect --in-row v0.1.0 --role link --name vfy-7`.
- **Gated release.** `$B click --role link --name fedcba9`; `$B expect --role heading --name "Production waits for you"`; `$B click --in-section "Production waits for you" --role button --name Reject --exact` shows `workflows are not running` (unverified locally).
- **Rolled back.** `$B goto /releases/00000000-0000-7000-8000-000000000603`; `$B expect --text "12 of 14 requests passed"`; `$B expect --text "profile/upload avatar png"`; `$B expect --role link --name vfy-13` (the bug issue); `$B expect --text "Release agent"`.
- **Missing release.** `$B goto /releases/00000000-0000-7000-8000-000000000699`; `$B expect --text "No such release"`.
- **Proof.** Screenshots under `$EVIDENCE_DIR/releases/` and `curl -s $API/api/releases/<id>`. The list endpoint needs the project: `curl -s "$API/api/releases?project=verify/demo"` (400 without it).

## Gotchas

- Everything but the gate is read-only; deployment, verification and the release agent are driven by Temporal and the Flux driver on the real factory. The seeded `v0.1.0` (succeeded with `production` pending) cannot happen in a real run; it is a render fixture.
- A stage waiting at its gate has no deployment row; the pipeline adds it from the pending approval of one of the release's runs.
- A finished release refreshes every 15 s, a live one every 3 s.
