# Runs and transcripts

`/projects/<prefix>/runs` lists a project's runs newest first: its issues' runs and the release agent's runs of its releases, each with its id, what it is for, status, when it started and its cost, filtered by `All`, `Active` or `Failed` (`?status=active|failed`). `/runs/<id>` is one run: a header with its workflow and issue (or `the release agent for release <name>`), start time, duration, a connection chip (`Connecting`, `Live`, `Reconnecting`, `Finished`), cost and tokens; the steps on the left (role, harness, model, duration, cost, attempts, loop-backs, skipped steps with their reason, steps not reached yet) and the selected step's transcript on the right (`?step=<step run id>`), streamed over server-sent events. A human gate waiting on the selected step is decided in place; a running agent step gets `Steer` and `Abort step`. At phone width the steps are a row of chips above the transcript. On a cold load the page shows a loading state until the run's steps and the run itself (`GET /api/runs/<id>`, D143) have answered, so it never calls a finished run live. The run comes by id, not from the run list, so a run older than the newest runs the list returns still shows its issue, start time, status and project.

## Sub-features

- `runs-list` the table (cards at phone width): `run <last 8>` link, the issue link or `release <name> · the release agent`, status chip, when it started (`just now`, `<n> min ago`, `<n> h ago`, `<n> days ago`), cost.
- `runs-filter` the `Show runs` radio group; `No failed runs` / `No active runs` with `Show all runs` when nothing matches.
- `run-steps` the `Steps` card (`the workflow snapshotted at start`), a `Run` entry for the run's own events, then each step as a link that selects it.
- `run-transcript` the selected step's events (agent messages, tool calls, command output, gate results, model usage, steers, aborts, errors) in a `Transcript` log.
- `run-status` the status badge in the heading and the connection chip: `Connecting` while the run's status is not known yet, `Live` for a running run, `Reconnecting` when its stream dropped, `Finished` for an ended one.
- `run-cold-load` before its data answers the page is a loading state; a finished run's page never reads `Live`, even when the run's read answers slowly.
- `run-outside-list` a run missing from `GET /api/runs` (older than the newest 200) keeps its issue link, start time, status badge and breadcrumbs, and the sidebar switches to its project.
- `run-requests` a load asks for each URL a few times at most: the stream's replay of stored events refreshes the page's data once per burst, not once per event.
- `run-gate` `This gate waits for a decision: <reason>` with `Approve`, and `Request changes` (needs a comment) and `Reject and close` when the gate allows changes, else `Reject` (needs a workflow).
- `run-steer` `Steer` and `Abort step` on a running agent step (needs a workflow).
- `run-missing` `No such run` for an unknown or malformed id.

## How to get to it (user POV)

- `Runs` in the sidebar.
- A run id on the Runs list, a latest-run status link on the board or a parent's `Issues` tab, `Open run` or a step chip on an issue page's run card, a run id on its `Runs` tab, `its run` in a waiting banner, `Run of <time>` on a release page.
- `/runs/<run id>`; the legacy `#/runs/<id>` redirects here.

## Driving it with browser.mjs

Preconditions: fresh launch (name and token set for the steer, abort and gate journeys). Seed runs: `00000000-0000-7000-8000-000000000207` (vfy-7, succeeded), `…0204` (vfy-4, running, one agent step), `…0206` (vfy-6, running, a human gate), `…2111` (vfy-11, running: loops, a skipped step, `review-style` running), `…2112` (vfy-12, running, waiting for an answer), `…2113` (vfy-13, failed), `…2115` (vfy-15, succeeded), `…0208` (release fedcba9, its production gate) and `…2303` (release runs). `E=$EVIDENCE_DIR/runs`.

- **List.** `$B goto /projects/vfy/runs`; `$B expect --role heading --name Runs --exact`; `$B expect --in-row vfy-7 --text succeeded`; `$B expect --in-row vfy-7 --text "days ago"`; `$B expect --in-row vfy-4 --text running`; `$B expect --in-row vfy-11 --text '$1.60'`; `$B expect --role link --name "release fedcba9"`; `$B expect --text "the release agent"`; `$B screenshot --path $E/list.png`.
- **Filter.** `$B click --role radio --name Failed` (`?status=failed`); `$B expect --in-row vfy-13 --text failed`; `$B expect --text vfy-7 --absent`; `$B click --role radio --name Active`; `$B expect --in-row vfy-4 --text running`; `$B expect --in-row vfy-11 --text running`; `$B expect --text vfy-7 --absent`. The other project: `$B goto /projects/oth/runs`; `$B expect --role link --name oth-3`; `$B expect --text vfy- --absent`.
- **Finished run page.** `$B goto /runs/00000000-0000-7000-8000-000000000207`; `$B expect --role heading --name "Run 00000207"` (the heading's full name is `Run 00000207 succeeded`); `$B expect --text Finished --exact`; `checks` is selected; `$B click --role link --name implement` (`?step=…`); `$B expect --role heading --name implement --exact`; `$B expect --text "Creating the Go module and the main package."`; `$B screenshot --path $E/run-207.png`.
- **Running run page.** `$B goto /runs/00000000-0000-7000-8000-000000000204`; `$B expect --text Live --exact`; `$B expect --text '$0.12'`; `$B expect --text "Model usage · $0.1234 · 6 turns"`; `$B expect --role button --name Steer --exact --disabled` (enabled once text is typed).
- **Steer and abort (expected locally).** On `…0204`: `$B fill --label "Steer the investigator" --value "look at the offset"`; `$B click --role button --name Steer --exact`; `$B expect --text "Could not steer: workflows are not running"`. `$B click --role button --name "Abort step"`; `$B expect --role alertdialog --name "Abort investigate?"`; `$B fill --label "Reason (required" --value verification`; `$B click --role button --name "Abort step"`; `$B expect --text "Could not abort: workflows are not running"`; `$B click --role button --name "Keep running"`.
- **Loops and skipped steps.** `$B goto '/runs/00000000-0000-7000-8000-000000002111?step=00000000-0000-7000-8000-000000311041'` (checks, attempt 1); `$B expect --role heading --name checks --exact`; `$B expect --text "command gate failed"`; `$B expect --text "looped back after checks failed"`; `$B expect --text "not reached yet"`; `$B expect --text "Open it to steer or abort it"` (another step runs); `$B expect --text Live --exact`; `$B screenshot --path $E/run-2111-checks.png`; `$B click --role link --name plan`; `$B expect --text "Skipped: it runs only when scope's needs_plan is true."`. The step list also shows `implement (attempt 2)` with `looped back after checks failed`.
- **Gate on the run page (expected locally).** `$B goto /runs/00000000-0000-7000-8000-000000000206`; `$B expect --text "This gate waits for a decision: Protected paths changed: .golangci.yml"`; `$B expect --role button --name "Reject and close"`; `$B expect --role button --name "Request changes" --disabled`; `$B click --role button --name Approve --exact`; `$B expect --text "workflows are not running"`. The release run `…0208` waits at a gate that allows no changes: `$B expect --role button --name Reject --exact`; `$B expect --role button --name "Reject and close" --absent`.
- **Failed and release runs.** `$B goto /runs/00000000-0000-7000-8000-000000002113`; `$B expect --text Finished --exact`; `$B expect --text failed`; `$B click --role link --name fix`; `$B expect --text "verifier aborted"`. `$B goto /runs/00000000-0000-7000-8000-000000002303`; `$B expect --role link --name "release b0b0b0b"`.
- **Missing.** `$B goto /runs/00000000-0000-7000-8000-000000000299`; `$B expect --text "No such run"`; `$B goto /runs/not-a-run`; `$B expect --text "No such run"`.
- **Cold load.** A finished run must not read `Live` while its read is slow, and a running one must once it answers. Hold `GET /api/runs/<id>` (the pattern also holds the list) and the run's stream for 6 s with `walk.mjs`'s `slow` (both: a finished run's stream ends at once and turns the chip `Finished` by itself, which hides the bug):

  ```json
  { "paths": [
    {"path": "/runs/00000000-0000-7000-8000-000000002113", "name": "cold-finished-C", "absent": ["Live"], "present": ["Finished"],
     "slow": [{"url": "/\\/api\\/runs(\\/[^/?]+)?(\\?|$)/", "ms": 6000}, {"url": "/\\/api\\/runs\\/[^/]+\\/stream/", "ms": 6000}]},
    {"path": "/runs/00000000-0000-7000-8000-000000002115", "name": "cold-finished-D", "absent": ["Live"], "present": ["Finished"],
     "slow": [{"url": "/\\/api\\/runs(\\/[^/?]+)?(\\?|$)/", "ms": 6000}, {"url": "/\\/api\\/runs\\/[^/]+\\/stream/", "ms": 6000}]},
    {"path": "/runs/00000000-0000-7000-8000-000000002112", "name": "cold-running-B", "present": ["Live"],
     "slow": [{"url": "/\\/api\\/runs(\\/[^/?]+)?(\\?|$)/", "ms": 6000}, {"url": "/\\/api\\/runs\\/[^/]+\\/stream/", "ms": 6000}]} ] }
  ```

  `node $S/walk.mjs "$UI" cold.json --out $E/cold --viewports desktop --themes light` passes; a page that reads `Live` too early fails with `showed "Live" at <n> ms` and `cold-finished-C.desktop.light.absent.png` shows it.
- **Outside the run list.** Answer `GET /api/runs` with `[]` through `walk.mjs`'s `mock`, as for a run older than the newest 200, and the page must still show the run's issue and project (Bugbot on #181: it showed neither, and the sidebar kept the last project):

  ```json
  { "paths": [
    {"path": "/runs/00000000-0000-7000-8000-000000002113", "name": "run-outside-list", "absent": ["Live"], "present": ["vfy-13", "Finished"],
     "mock": [{"url": "/\\/api\\/runs(\\?|$)/", "json": []}]},
    {"path": "/runs/00000000-0000-7000-8000-000000002403", "name": "run-outside-list-project", "viewports": ["desktop"],
     "mock": [{"url": "/\\/api\\/runs(\\?|$)/", "json": []}], "present": ["oth-3", {"role": "button", "name": "Project: other. Switch project"}]} ] }
  ```

  It passes; a page that finds its run only in the list fails with `never showed "vfy-13"` and `never showed {"role":"button","name":"Project: other. Switch project"}`. The sidebar is a closed sheet at phone width, so the project check names `"viewports": ["desktop"]`. Report that these paths used a mock.
- **Request budget.** Every `walk.mjs` load fails when one GET `/api` URL is asked for more than 4 times before the page settles. The run pages are the ones a stream replays into: walk `/runs/…2111`, `…2112`, `…2113`, `…2115` and `…0207`; each asks at most 3 times for any URL. A page that refreshes per replayed event asks 5-10 times and fails with `GET /api/runs/<id>/steps <n> times (budget 4)`; `report.json` lists every URL asked for more than once under `requests`.
- **Back, forward, reload.** A `walk.mjs --history` file with `{"goto": "/projects/vfy/runs"}`, `{"click": {"role": "radio", "name": "Failed"}}`, `{"click": {"role": "radio", "name": "All"}}`, `{"click": {"role": "link", "name": "run 00002111"}}`, `{"click": {"role": "link", "name": "scope"}}`, `{"click": {"role": "link", "name": "implement (attempt 2)"}}` passes: `?status=failed`, then `?step=…311011` and `?step=…311032`.
- **Proof.** Screenshots of the list and the run pages under `$EVIDENCE_DIR/runs/`, `history/history.json`, plus `curl -s $API/api/runs/<id>` and `curl -s $API/api/runs/<id>/steps`.

## Gotchas

- The connection chip is `role=status` without a name: expect its text (`Live`, `Finished`) with `--exact`. `Live` needs the run's status from `GET /api/runs/<id>`; until then the page is loading, and if that request fails a running run stays `Connecting`.
- The heading includes the status badge (`Run 00000207 succeeded`): match `Run <last 8>` without `--exact`.
- A live run page polls the run and its steps every 3 s and the approvals every 5 s; with `slow` longer than about 6 s a live page's polls can add up against the request budget, so keep `ms` at 6000 or set `"budget"` on that path.
- The legacy `#/runs/<id>` redirect takes a full run id only; `#/runs` opens the project's runs list.
- A run with nothing in `?step=` selects its running step, else its last; step links prefix-match (`implement` matches `implement (attempt 2)` too, first wins), so pick a step by `?step=<id>` when the attempt matters. A step run id ends in `3` + issue number + step number + attempt (`…311032` is vfy-11's step 03, attempt 2).
- The steps list and transcript depend on `/api/runs/<id>/steps` and the SSE stream `/api/runs/<id>/stream`; for a running run the stream stays open, so it never turns `Finished`. No events arrive after the seed.
- `Steer`, `Abort step` and gate decisions signal Temporal; with the token set they fail locally with `workflows are not running` (without it the `API token` dialog opens). They are unverified here.
- Runs created from the seed have fixed ids; a real run's id is a UUID you read from the Runs list link.
