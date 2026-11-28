# Runs and transcripts

The Runs page lists every run (issue runs and release-agent runs) with status, cost and creation time. A run page shows the live transcript: a steps table with each step's role, harness, model, status and cost, then the run's events in plain words under a heading per step, streamed over server-sent events. A running agent step also gets a steer and abort panel.

## Sub-features

- `runs-list` lists runs newest first (fetched once, no polling): an issue run's key and a release run's `release <name>` link to the run page; a release run reads `the release agent · release`, with `release` linking to the release.
- `run-steps` shows the steps table with a total row.
- `run-transcript` shows events (agent messages, tool calls, command output, gate results, state changes) under step headings.
- `run-status` shows `live` for a running run and `finished: <status>` for a finished one.
- `run-steer` steers or aborts a running agent step (needs a workflow).

## How to get to it (user POV)

- Open `#/` (the Runs page; also the `Runs` link in every header).
- Click a run's issue key on the Runs page, a run link (`running`, `succeeded`) on the board, a run id link (last 8 characters) or `full transcript` on an issue page, or a gate's key in the approvals inbox.
- Open `#/runs/<run id>`.

## Driving it with browser.mjs

Preconditions: fresh launch. Seed runs: `00000000-0000-7000-8000-000000000207` (vfy-7, succeeded, finished), `...0204` (vfy-4, running, one agent step), `...0206` (vfy-6, running, a human gate), `...0208` (release `fedcba9`, running, its `production` gate).

- **List.** `$B goto '#/'`; `$B expect --role heading --name Runs`; `$B expect --in-row vfy-7 --text succeeded`; `$B expect --in-row vfy-4 --text running`; `$B expect --text "release fedcba9"`; `$B expect --text "the release agent"`. This part works.
- **Finished run page.** `$B goto '#/runs/00000000-0000-7000-8000-000000000207'`; `$B expect --text "finished: succeeded"`; `$B expect --role table` with the `implement` and `checks` rows; `$B expect --text "Creating the Go module and the main package."`.
- **Running run page.** `#/runs/00000000-0000-7000-8000-000000000204`: `$B expect --text live`; the steps table row `investigate` with `$0.12` (the table rounds to cents; `$0.1234 · 6 turns` is in the transcript's model usage line); `Running: investigate` panel with `Steer` and `Abort` disabled until text is typed.
- **Proof.** Snapshot and screenshot of the run page under `$EVIDENCE_DIR/runs/`, plus `curl -s $API/api/runs/<id>/steps`.

## Gotchas

- Known failure, observed 2026-11-27 and again 2026-11-28 (main `c43ca62`) against Chromium 159 (Playwright 1.65.1, `chromium-1255`): every `#/runs/<id>` page renders blank. `RunPage` has `useEffect(() => bottom.current?.scrollIntoView({ block: "end" }), [events.length])` (`web/src/RunPage.tsx`); in Chromium 159 `scrollIntoView` returns a Promise, so React 19 throws `destroy is not a function` and unmounts the page (console: `must not return anything besides a function`). The run-page journeys therefore report FAIL until the effect stops returning the call's value; the Runs list and the issue page are unaffected. Do not weaken the journeys to pass. Check with `node $S/browser.mjs text` (empty body) after `goto`.
- The steps table and transcript depend on `/api/runs/<id>/steps` and the SSE stream `/api/runs/<id>/stream`; for a running run the stream stays open, so `finished: ...` never appears. No events arrive after the seed.
- `Steer` and `Abort` signal Temporal and, with the token set, fail locally with `workflows are not running` (without it: `missing or wrong API token`); `Abort` opens a `window.confirm` (`click --accept-dialog`).
- Runs created from the seed have fixed ids; a real run's id is a UUID you read from the Runs list link.
