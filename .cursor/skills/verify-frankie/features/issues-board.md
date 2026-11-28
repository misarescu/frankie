# Issues board

`/projects/<prefix>/board` shows a project's issues: a strip with what is running, what waits for you, what merged and what everything spent, then the issues as their hierarchy (epic, story, task/bug) with status, what each waits for, cost (a parent its total with its own beneath, D142), change request and latest run, or as a dependency graph (`?view=graph`). Finished issues stay hidden until `Show finished` (`?finished=1`). An operator ticks issues and asks Frankie to start them; the scheduler would then run them in dependency order. At phone width each row is a card.

## Sub-features

- `board-list` the tree with status chips, `Waits for` links (issues, and `a gate decision`, `an answer`, `spec approval` linking to the inbox), costs, change request numbers and latest-run links.
- `board-finished` hides merged, released and canceled issues until `Show finished` is ticked; their checkboxes are disabled. A `failed` issue stays visible and selectable.
- `board-graph` the `Dependencies` view: a figure `issue dependency graph` whose nodes link to their issues.
- `board-start` starts the selected issues: a status panel names what was requested, what starts on the scheduler's next pass and what waits, and the row shows `requested`.
- `board-start-parent` starting a parent covers everything under it.
- `board-no-token` a start without the API token is refused and opens the `API token` dialog.
- `board-switch` the sidebar's project switcher keeps the page and its view for the other project (`/projects/oth/board`).
- `board-nav` links to an issue page, a run page and, from the sidebar, `Runs`, `Releases` and `Approvals`.

## How to get to it (user POV)

- `/` opens the last project's board (`frankie.project`, else the first project); `Board` in the sidebar; the project crumb on an issue or run page.
- The legacy `#/issues` redirects here.

## Driving it with browser.mjs

Preconditions: fresh launch, doctor clean, name and token set (`setup` block in SKILL.md). The seed has `vfy-2` ready with no start request. `E=$EVIDENCE_DIR/issues-board`.

- **List.** `$B goto /projects/vfy/board`; `$B expect --role heading --name Board`; `$B expect --in-row vfy-3 --role link --name vfy-2 --exact` (vfy-3 waits for vfy-2); `$B expect --role link --name vfy-4`; `$B expect --in-row vfy-5 --text requested --exact`; `$B expect --in-row vfy-6 --role link --name "a gate decision"`; `$B expect --in-row vfy-12 --role link --name "an answer"`; `$B expect --in-row vfy-5 --role link --name "spec approval"`; `$B expect --in-row vfy-8 --text '$4.27'` (a parent's total); `vfy-7` (merged) is absent: `$B expect --text vfy-7 --absent`.
- **Finished toggle.** `$B check --label "Show finished"` (URL gains `?finished=1`); `$B expect --in-row vfy-7 --text merged`. Untick it with `$B click --label "Show finished"` (`check` only ticks), then `$B expect --text vfy-7 --absent` (an `--in-row` locator with `--absent` fails when the row itself is gone).
- **Graph.** `$B click --role radio --name Dependencies` (`?view=graph`); `$B expect --role figure --name "issue dependency graph"`; `$B screenshot --path $E/graph.png`; back with `$B click --role radio --name List`.
- **Start one.** Before: `$B snapshot --path $E/before.aria.txt`, `$B screenshot --path $E/before.png`, `$S/frankie-verify sql "select start_requested_by from issues where key='vfy-2'"` is empty. `$B expect --role button --name "Start selected" --disabled`. Action: `$B check --role checkbox --name "select vfy-2"`; `$B expect --role button --name "Start 1 selected" --enabled`; `$B click --role button --name "Start 1 selected"`. Result: `$B expect --text "Start requested: vfy-2"` (the panel is `--role status`) and `$B expect --in-row vfy-2 --text requested --exact`. After: snapshot and screenshot as `after.*`.
- **Second view.** `$S/frankie-verify sql "select start_requested_by from issues where key='vfy-2'"` prints `verifier`; `curl -s $API/api/issues/vfy-2` has `"start_requested_by":"verifier"`.
- **Start a parent.** After the previous step in the same launch: `$B check --role checkbox --name "select vfy-1" --exact` (without `--exact` it matches `select vfy-10` too), `$B click --role button --name "Start 1 selected"`; `$B expect --text "Start requested: vfy-1"`; `$B expect --text "Starting on the scheduler's next pass: vfy-2"`; `$B expect --text "vfy-5 waits: spec waits for approval"`; `$B expect --text "vfy-3 waits: waits for vfy-2"` (the waiting lines come in no fixed order; expect each on its own); `$B expect --in-row vfy-5 --text requested --exact`. Screenshot `$E/start-parent.png`.
- **Without a token.** `$B click --role button --name "Your name and API token"`, `$B click --role menuitem --name "Forget the API token"`, `$B expect --text "No API token: read only"`; `$B check --role checkbox --name "select vfy-10"`; `$B click --role button --name "Start 1 selected"`; `$B expect --role dialog --name "API token"` and `$B expect --text "The API needs a valid token for that."`; `$B click --role button --name Cancel`, then `$B expect --text "Could not start vfy-10"` (the line goes on `this needs the API token; set it under API token, in the menu at the bottom of the sidebar`). `sql` shows no start request on vfy-10. Set the token again before anything else: `$B click --role button --name "Your name and API token"`; `$B click --role menuitem --name "Set the API token"`; `$B fill --label "API token" --exact --nth 1 --value "$API_TOKEN"`; `$B click --role button --name Save`; `$B expect --text "API token set"`.
- **Switch project.** `$B click --role button --name "Switch project"`; `$B click --role menuitem --name oth`; `$B expect --role link --name oth-2`; `$B expect --text vfy-2 --absent`.
- **Navigate.** `$B goto /projects/vfy/board`; `$B click --role link --name vfy-4` opens `/issues/vfy-4` (`$B expect --role heading --name "Pagination returns duplicates"`); from the board, `$B goto /projects/vfy/board`; `$B click --in-row vfy-4 --role link --name running` opens its latest run (`$B expect --role heading --name "Run 00000204"`); the sidebar links `Runs`, `Releases` (both with `--exact`) and `Approvals` open those pages.
- **Back, forward, reload.** A `walk.mjs --history` file with `{"goto": "/projects/vfy/board"}`, `{"click": {"label": "Show finished"}}`, `{"click": {"role": "radio", "name": "Dependencies"}}`, `{"click": {"role": "link", "name": "vfy-14"}}` (a graph node), `{"click": {"role": "link", "name": "Board", "exact": true}}` (the crumb), `{"click": {"role": "link", "name": "vfy-4"}}` passes: `?finished=1`, then `?finished=1&view=graph`, each restored by back, reload and forward.
- **Proof.** The `board-start` journey above: `before`/`after` snapshot and screenshot, `db-*.txt`, API JSON and `history/history.json` under `$E`.

## Gotchas

- The start panel can say `Starting on the scheduler's next pass` for a startable issue, but no scheduler runs here, so nothing starts: the proof of a start request is the `requested` marker and the `start_requested_*` columns, not a run appearing. A real start (a run appearing, status `running`) is unverified without Temporal.
- Link and checkbox names are prefixes: `vfy-1` also matches `vfy-10`..`vfy-18`. Use `--exact` on `select vfy-1` and pick keys that are not a prefix of another (`vfy-4`, `vfy-6`).
- `Show finished` lives in the list's card; the `Dependencies` view has no toggle of its own, so tick it in the list first (or open `?view=graph&finished=1`).
- Without a name a start succeeds but is recorded as `web`. Set both name and token first.
- The seed's `maxConcurrentIssues` is 5 and several issues are already running; if you see `no free slot`, the config was changed.
- The board refreshes every 10 s; `goto` reloads the page so state is fresh.
- Selecting then starting mutates the seed; the next feature that expects an un-requested `vfy-2` needs a fresh launch.
