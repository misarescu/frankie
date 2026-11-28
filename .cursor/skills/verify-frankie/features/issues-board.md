# Issues board

The board lists a project's issues as their hierarchy (epic, story, task/bug) with status, what each waits for, cost and its latest run, plus a dependency graph. An operator selects issues and asks Frankie to start them; the scheduler would then run them in dependency order.

## Sub-features

- `board-list` shows the project's issues as a tree with status chips, `waits for` links, costs and run links.
- `board-finished` hides finished issues (merged, released, canceled) until `show finished` is ticked; their checkboxes are disabled. A `failed` issue stays visible and selectable.
- `board-graph` draws the dependency graph (`issue dependency graph`) for issues that wait for others.
- `board-start` starts the selected issues: a panel names what was requested and what waits, and the row shows `requested`.
- `board-start-parent` starting a parent covers everything under it.
- `board-nav` links to an issue page, a run page and the Runs, Releases, Approvals pages.

## How to get to it (user POV)

- Open `#/issues` (the "Issues" link in the header of the Runs, Releases and Approvals pages).
- Follow an issue key link or an issue page's `Issues` breadcrumb back to the board.

## Driving it with browser.mjs

Preconditions: fresh launch, doctor clean, name and token set (`setup` block in SKILL.md). The seed has `vfy-2` ready with no start request.

- **List.** Open the board. `$B goto '#/issues'`; `$B expect --role heading --name Issues`; `$B expect --in-row vfy-3 --text vfy-2` (vfy-3's `Waits for` cell links vfy-2). The tree shows `vfy-1`, `vfy-2`, `vfy-3`, `vfy-5` (`$B expect --in-row vfy-5 --text requested`), `vfy-4`, `vfy-6`; `vfy-7` (merged) is absent (`$B expect --text vfy-7 --absent`). `$B expect --role img --name "issue dependency graph"` shows the graph.
- **Finished toggle.** `$B check --label "show finished"`; `$B expect --in-row vfy-7 --text merged`. Untick it with `$B click --label "show finished"` (`check` only ticks), then `$B expect --text vfy-7 --absent` (an `--in-row` locator with `--absent` fails when the row itself is gone).
- **Start one.** Before: `$B snapshot --path $E/before.aria.txt`, `$B screenshot --path $E/before.png`, `$S/frankie-verify sql "select start_requested_by from issues where key='vfy-2'"` is empty. Action: `$B check --role checkbox --name "select vfy-2"`; `$B expect --role button --name "Start 1 selected" --enabled`; `$B click --role button --name "Start 1 selected"`. Result: `$B expect --text "Start requested: vfy-2"` and `$B expect --in-row vfy-2 --text requested`. After: snapshot and screenshot as `after.*`.
- **Second view.** `$S/frankie-verify sql "select start_requested_by from issues where key='vfy-2'"` prints `verifier` (the "Acting as" name); `curl -s $API/api/issues/vfy-2` has `"start_requested_by":"verifier"`.
- **Start a parent.** After the previous step in the same launch, `$B check --role checkbox --name "select vfy-1"`, `$B click --role button --name "Start 1 selected"`; `$B expect --text "Start requested: vfy-1"`. The panel then says `Starting on the next pass: vfy-2` and lists what waits: `vfy-5: spec waits for approval` and `vfy-3: waits for vfy-2` (in no fixed order; expect each line on its own); the `vfy-1` and `vfy-5` rows read `requested`. Screenshot `$E/start-parent.png`.
- **Navigate.** `$B click --role link --name vfy-4` goes to `#/issues/vfy-4`; `$B goto '#/issues'`; `$B click --role link --name running --nth 0` goes to a `#/runs/<id>` page (which crashes on Chromium 159, see `runs.md`).
- **Proof.** The `board-start` journey above: `before`/`after` snapshot and screenshot, `db-*.txt`, API JSON under `$E` (`$E=$EVIDENCE_DIR/issues-board`).

## Gotchas

- Without the token a start is refused: the board's error line says `this needs the API token; set it at the top of the page` and the token bar opens with `The API needs a valid token for that.` (verified 2026-11-28; nothing is written). Without a name the start succeeds but is recorded as `web`. Set both first.
- The "Start requested" panel can say `Starting on the next pass` for a startable issue, but no scheduler runs here, so nothing starts: the proof of a start request is the `requested` marker and `start_requested_*` columns, not a run appearing. A real start (a run appearing, status `running`) is unverified without Temporal.
- The seed's `maxConcurrentIssues` is 5 and two issues are already running; if you see `no free slot`, the config was changed.
- The board refreshes every 10 s; `goto` reloads the page so state is fresh.
- Selecting then starting mutates the seed; the next feature that expects an un-requested `vfy-2` needs a fresh launch.
