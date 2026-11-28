# M9 acceptance (2026-11-28)

M9 rebuilt the web UI (`docs/spec/10-roadmap.md`, D141, D142). This note records how each done-when criterion was checked. Evidence lives outside the repo in `~/.frankie-verify/evidence/m9-acceptance/` on the machine that ran it.

## Seeded control plane (verify-frankie)

The verify-frankie skill runs the real control plane binary on a throwaway Postgres with `scripts/seed.sql`. The seed now covers every page state: an epic > stories > tasks tree with costs at each level and a canceled child, a run that loops back after a failed `checks` gate with a skipped conditional step and steps not reached yet, every transcript event type and gate result type, a waiting question, a human gate, a refined spec, a release rolled back with failed tests and a bug issue, a release in progress, a release waiting at its production gate, a 200-character title, and a second project.

- `scripts/walk.mjs` loaded 117 URLs (every seeded issue and tab, all 15 runs with 17 selected-step cases, every filter and view, unknown keys and ids, the old hash URLs) at desktop and phone width in light and dark: 468 page loads, no console errors, no unexpected API errors, every page with a heading.
- A history walk (board > issue > tab > run > step > step > approvals > filter > release) checked back, forward and reload at each step, in both themes.
- The same walk with the control plane stopped: every page says what it could not load.
- Every write that touches only Postgres was driven from the UI and checked in the database: start, start a parent, retry, complete, cancel (and a refused cancel), approve and send back a refined spec, refine again. Gate decisions, answers, steers and aborts reach the API and show its answer inline; without Temporal they cannot take effect there.

## The real factory

The new UI ran locally against Kapsule's control plane (61316b0) through a port-forward.

- A read-only walk of 60 URLs (every page of Goodreads-Clone, frankie and frankie-dummy, 12 runs, 6 releases) found two bugs the seed could not show, both fixed: a parent's page failed when the API sent no `total_cost_usd` (the deployed control plane predates D142; the queries now fill it from the issue's own cost), and a stage without a verify step read "verification succeeded".
- **Live run:** Goodreads-Clone#162 synced as gc-80 and was started from the board with "Start 1 selected" at 14:39Z, recorded as requested by the name set in the UI. Its `build-e2e` run was watched on the board, the issue page and the run page, with a screenshot every 90 seconds: the e2e writer and the coder ran in parallel, `checks` failed and the run looped back to `implement`, a steer sent from the run page reached that session and shows in its transcript, the review passed, the change request (#163) passed 409 tests in its preview and the repo's CI, and the merge queue merged it at 15:06Z. 27 minutes, $0.54. Its release `09af06d` passed staging (409 tests) and waited at the production gate, shown in place on the release page and in the inbox. It was approved from the release page, deployed to production and published as `v0.2.0`; gc-80 is `released`.
- The production image under the web pod's limits (256 MiB, 0.25 CPU) answers 2.6 s after start and uses 50-54 MiB while the pages poll and stream.

## Found in review

Cursor Bugbot found that a cold load of a finished run's page read "Live" until the run list answered. Reproduced on the real factory by slowing `GET /api/runs`. The same repro found that the stream's replay refetched the run's queries once per stored state change (42 steps requests on one load). Both are fixed. `walk.mjs` can now slow chosen API routes, check text that must never show during a load, and fails a page that requests one URL more than 4 times while it loads. Each check failed on the code before the fix. Bugbot's second finding, that a run older than the newest 200 lost its issue and project on its page, led to `GET /api/runs/{id}` (D143); a walk with the run list mocked empty checks it. Loading and failure states were added for gates and the inbox.

## Not verified

- Totals on the real factory: Kapsule runs a control plane from before D142, so its parents show their own cost until this change is deployed. Totals are checked on the seeded control plane and by the store and API tests.
- Aborting a live step and answering a live question from the new UI; their requests and error handling are checked on the seeded instance.
