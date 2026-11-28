---
name: verify-frankie
description: Launch Frankie's web UI (React + Bun, `web/`) with a real control plane API on a throwaway Postgres, drive it in a headless browser the way an operator does (issues board, issue page, approvals inbox, runs, releases) and capture proof. Use when you changed `web/` or `internal/api/` and need evidence the UI works, or before saying a Frankie UI/API change is done. It does not start the cluster, Temporal or agents.
---

# Verify Frankie

Frankie is a software factory: a Go control plane (API, Temporal worker, git proxy) plus a React/Bun web UI in `web/` that operators use to watch runs, start issues, answer agents and decide gates. The UI is the primary surface. This skill starts the real control plane binary and the real web server against a disposable Postgres seeded with a demo project, then drives the UI in headless Chromium.

What runs: Postgres (Docker) + `cmd/controlplane` (no Temporal, no Kubernetes) + `web/server.ts` + Chromium. What does not: workflows, agent pods, the scheduler, GitHub intake, LiteLLM. So UI reads and the writes that only touch Postgres are provable here (start request, cancel, complete, spec approve/reject). Anything that needs a live workflow (human gate decision, answer, steer, abort, retry, a real run progressing, releases deploying) answers "workflows are not running" and is **not** verified by this skill; say so instead of faking it. The factory itself lives on Kapsule (`kubectl` context `admin@frankie-k8s-v2`): never point this skill at it, never change it, never schedule pods on its platform node.

Helpers live in `scripts/` next to this file. Use paths relative to the repo root: `S=.cursor/skills/verify-frankie/scripts`. Claude Code loads this same skill through the symlink `.claude/skills/verify-frankie`; edit the files here, under `.cursor/`.

## Launch

```bash
S=.cursor/skills/verify-frankie/scripts
mise exec -- $S/frankie-verify launch     # ~30 s warm; prints UI, API, API_TOKEN, EVIDENCE_DIR
```

Run it under `mise exec --` so it gets the pinned Bun: an older `bun` on the PATH cannot read `web/bun.lock` and the launch stops at the web UI.

Needs: `docker` (running), `mise` (pinned Go), `bun`, `node`/`npm`, `perl`, `lsof`, `curl`. First run also installs Playwright + Chromium into `~/.cache/frankie-verify/playwright` (outside the repo, once).

What it does, in order, from an empty state each time:

1. Picks free ports and a run id; state goes to `${TMPDIR}/frankie-verify/runs/<id>/`, evidence to `~/.frankie-verify/evidence/<id>/`.
2. Starts `postgres:17` as container `frankie-verify-pg-<id>` on `127.0.0.1:<port>` and waits for a TCP query to succeed (not `pg_isready`: the image's init scripts run a socket-only server first).
3. `go build`s `cmd/controlplane` stamped with `git rev-parse HEAD` and starts it with only `FRANKIE_DATABASE_URL`, a random git-proxy key and a random API token (D58). The control plane migrates the database and creates projects `verify/demo` (key prefix `vfy`) and `verify/other` (`oth`) from a generated config, so the project switcher has two. Ready means `/readyz` is 200 **and** `/api/projects` lists both.
4. Applies `scripts/seed.sql` (verification scaffolding, never run it on a real database): issues `vfy-1`..`vfy-18` and `oth-1`..`oth-3`, runs with transcripts covering every step kind and event type, a pending human gate on `vfy-6`, a question waiting on `vfy-12`, a refined spec waiting on `vfy-5`, and four releases (succeeded, waiting at its production gate, rolled back, deploying). `features/README.md` lists every seeded key and id and what it demonstrates.
5. Starts `bun server.ts` in `web/` (serves the UI, proxies `/api` to the control plane) and a headless Chromium with a CDP port.

Everything it starts is detached into its own session, so it survives the shell that launched it. Pids are in `state.env` in the state dir. The checkout is built as-is: uncommitted `internal/` changes are compiled in (the stamped commit is still HEAD), `web/` is served live.

Isolation: ports, container, database and browser profile are per run, so another instance can run beside it with `FRANKIE_VERIFY_RUN=<other id> $S/frankie-verify launch`. A plain second `launch` refuses while a `current` instance exists. Do not drive an instance you did not start in this task (check `frankie-verify env` shows your run id).

## Doctor

```bash
$S/frankie-verify doctor
```

Read-only. Checks that both processes are ours and own their ports, the Postgres container is running, `/api/version` equals the commit it was launched at, `/readyz` is 200, the UI's `/api` proxy reaches the control plane and returns at least 18 seeded issues, the API token is accepted (a 400 on an empty start request, 401 would mean rejected) and the browser answers. Exit 0 and no `FAIL` lines means worth driving. Run it first whenever anything looks off; on a `FAIL`, `cleanup` and `launch` again rather than patching a half-dead instance. It warns (does not fail) when HEAD moved since launch: Go changes then need a relaunch, `web/` changes do not.

`frankie-verify env` reprints the URLs, token and evidence directory. `frankie-verify sql '<query>'` runs a read-only query in the instance's Postgres (use it to verify side effects).

## Drive

Browser commands are `node $S/browser.mjs <cmd>`: a persistent Chromium, each command attaches, acts, detaches, so route, localStorage (`frankie.apiToken`, `frankie.name`, `frankie.theme`, `frankie.project`) and page state carry across commands. Run `node $S/browser.mjs` with a bad command to see usage; the header of `scripts/browser.mjs` is the reference.

```
goto </path | #/route | url>               /projects/vfy/board, /issues/vfy-11, /runs/<id>, /approvals, ... (M9 path URLs, features/README.md)
click|check|fill|expect|text <locator>     fill takes --value; expect takes --absent --value --enabled --disabled --checked
snapshot --path f.aria.txt | screenshot --path f.png [--full]
locator: --role R --name N | --text T | --label L | --placeholder P   [--in-row TEXT | --in-section TEXT] [--exact] [--nth I]
click --accept-dialog                      for window.confirm (Complete, Abort)
```

Set `FRANKIE_VERIFY_FEATURE=<feature id>` in the shell so each action in `actions.log` is tagged. `expect` exits 1 on failure; every failure message is the reason, report it as is. Default timeout 10 s (`FRANKIE_VERIFY_TIMEOUT_MS`). Prefer roles and accessible names: segmented filters and views are `radio`s (`Dependencies`, `Failed`, `Gates`), issue sections are `tab`s, dialogs are `dialog` or `alertdialog` named by their title, cards and banners are `<section>`s for `--in-section`, board checkboxes are `select <key>`. A dialog's title also labels it, so `--label <title>` can match the dialog before its field: use `--role textbox --name`, `--placeholder` or `--nth 1`.

First thing in a fresh browser, set your name and the API token like an operator does, from the menu at the foot of the sidebar (the UI sends both with every write). Writes fail without the token; without the name they succeed but are recorded as `web`, so the recipes' checks for `verifier` fail:

```bash
export FRANKIE_VERIFY_FEATURE=setup
node $S/browser.mjs goto /projects/vfy/board
node $S/browser.mjs click --role button --name "Your name and API token"
node $S/browser.mjs click --role menuitem --name "Set your name"
node $S/browser.mjs fill --role textbox --name "Your name" --value verifier
node $S/browser.mjs click --role button --name Save
node $S/browser.mjs click --role button --name "Your name and API token"
node $S/browser.mjs click --role menuitem --name "Set the API token"
node $S/browser.mjs fill --label "API token" --exact --nth 1 --value "$API_TOKEN"   # from `frankie-verify env`
node $S/browser.mjs click --role button --name Save
node $S/browser.mjs expect --text "API token set"
node $S/browser.mjs expect --text verifier --exact
```

`--nth 1` skips the dialog, which its title `API token` also labels; keep `--label "API token"` so `actions.log` records `<token>` instead of the value (a `--placeholder` fill logs it). Once set, the menu items read `Change your name` and `Change the API token`, and `Forget the API token` clears it.

The feature map is in `features/README.md`: read it, then the file for the feature you are verifying. A proof that drives one entry point is incomplete when the map lists others.

## Walk every page

`scripts/walk.mjs` loads a list of paths in a fresh headless Chromium for each viewport (desktop 1440x900, phone 390x844) and theme (light, dark: `localStorage frankie.theme` is set before the page loads), waits until no request is in flight (event streams excepted), takes a full-page screenshot and records console errors, uncaught page errors and `/api` responses with status 400 or more. It writes `report.json` next to the screenshots and exits 1 on anything the paths file does not expect. It knows no page; per-page checks belong to the recipes.

```bash
UI=$($S/frankie-verify env | sed -n 's/^UI=//p')
cat > /tmp/paths.json <<'EOF'
{ "paths": ["/projects/vfy/board", "/issues/vfy-14",
            {"path": "/issues/vfy-99", "name": "issue-missing", "expect": [{"url": "/api/issues/vfy-99", "status": 404}]}] }
EOF
node $S/walk.mjs "$UI" /tmp/paths.json --out "$EVIDENCE_DIR/walk"     # --viewports desktop --themes dark to narrow
```

`--history` takes `{"steps": [{"goto": "/projects/vfy/board"}, {"click": {"role": "link", "name": "vfy-11"}}, ...]}`, runs the steps recording each URL and the page's first `h1`, then goes back step by step (reloading at each) and forward again, and fails when a URL or `h1` does not come back or a click did not change the URL (`"sameUrl": true` allows it). The file header of `walk.mjs` is the reference for both formats; `"storage"` sets more localStorage keys (`frankie.apiToken`, `frankie.project`) before every load.

## API down

```bash
$S/frankie-verify api stop     # kills only the control plane pid it launched; the web server then answers /api with 500
$S/frankie-verify api start    # same ports, token and database; waits until it is ready again
```

Use it to show the UI's error states, then start it again before driving anything else; `doctor` fails while it is stopped. In a walk, list the failures as expected: `"expect": [{"url": "/api/", "status": 500}]`.

## Evidence

Everything goes under `EVIDENCE_DIR` (`~/.frankie-verify/evidence/<run id>/`), one subfolder per feature id. Cleanup keeps this directory and copies the process logs into `logs/`.

Proof standards:

- Exercise the real operator path: click the buttons, fill the fields. Do not call `/api` writes with curl to stand in for the UI, and do not poke rows into Postgres (the seed is the only SQL write).
- Capture the action and the resulting state: a before snapshot or screenshot, the click, an after snapshot and screenshot. `actions.log` records each browser command with a timestamp.
- Verify side effects beside what is visible: `frankie-verify sql` for the row (e.g. `start_requested_by`) and `curl $API/api/issues/<key>` for what the API returns. A UI success banner alone is not proof.
- Screenshots must show the app identity (the page heading and the sidebar, whose footer shows your name and `API token set`, are in frame).
- Mocks: none inside the app. The only stand-ins are the absent Temporal, Kubernetes and GitHub; decisions that need Temporal fail with "workflows are not running" and count as unverified.
- Record what was not verified (anything behind a workflow, the live cluster, the homelab, Tailscale, real agents) in the report.

## Cleanup

```bash
$S/frankie-verify cleanup
```

Copies logs into `EVIDENCE_DIR/logs`, stops the browser, kills the web and control plane pids it recorded (never by name), removes the Postgres container and the state dir. Evidence stays. Run it after a failed attempt too. Afterwards `docker ps -a --filter name=frankie-verify` and `lsof -nP -iTCP -sTCP:LISTEN` for your ports should show nothing of ours. Kill a stray you started by pid only.

## Helpers

| Command | What |
|---|---|
| `scripts/frankie-verify launch \| doctor \| env \| sql '<query>' \| api stop\|start \| cleanup` | instance lifecycle, shown above |
| `node scripts/walk.mjs [--history] <base-url> <file.json> [--out dir]` | every page in both viewports and themes, or a back/forward/reload walk; `report.json` or `history.json` |
| `node scripts/browser.mjs start \| status \| stop \| goto \| click \| fill \| check \| expect \| text \| snapshot \| screenshot` | browser driver, shown above (`start`/`stop` are called by launch/cleanup) |
| `scripts/seed.sql` | rows for both demo projects, applied by launch |

Environment overrides: `FRANKIE_VERIFY_STATE`, `FRANKIE_VERIFY_EVIDENCE`, `FRANKIE_VERIFY_RUN`, `FRANKIE_VERIFY_PLAYWRIGHT`, `FRANKIE_VERIFY_TIMEOUT_MS`.

## Other surfaces

- API: the same control plane. `curl $API/api/...`: reads are open (`/api/projects`, `/api/runs`, `/api/runs/<id>/steps`, `/plan`, `/events`, `/api/issues?project=verify/demo`, `/api/issues/<key>`, `/api/approvals`, `/api/questions`, `/api/specs`, `/api/releases?project=verify/demo` (400 without `project`), `/api/version`), writes need `Authorization: Bearer $API_TOKEN` and JSON bodies. Use it for second views, not as a replacement for the UI path.
- `go test ./...` and `bun test` in `web/` (`mise run test`, `mise run lint`) are the code-level checks; this skill is the end-to-end check on top.
- The real factory (port-forward `svc/frankie-web`, token from `mise run api-token`) is for looking, not driving; it is shared and runs real agents.
- `cmd/factoryctl` and the cluster `e2e/` suite need the cluster and are out of scope here.

Maintain the feature map with `/maintain-verification-skill` (`/pstack:maintain-verification-skill` in Claude Code) when routes, labels or seed expectations change.
