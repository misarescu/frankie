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
$S/frankie-verify launch     # ~30 s warm; prints UI, API, API_TOKEN, EVIDENCE_DIR
```

Needs: `docker` (running), `mise` (pinned Go), `bun`, `node`/`npm`, `perl`, `lsof`, `curl`. First run also installs Playwright + Chromium into `~/.cache/frankie-verify/playwright` (outside the repo, once).

What it does, in order, from an empty state each time:

1. Picks free ports and a run id; state goes to `${TMPDIR}/frankie-verify/runs/<id>/`, evidence to `~/.frankie-verify/evidence/<id>/`.
2. Starts `postgres:17` as container `frankie-verify-pg-<id>` on `127.0.0.1:<port>` and waits for a TCP query to succeed (not `pg_isready`: the image's init scripts run a socket-only server first).
3. `go build`s `cmd/controlplane` stamped with `git rev-parse HEAD` and starts it with only `FRANKIE_DATABASE_URL`, a random git-proxy key and a random API token (D58). The control plane migrates the database and creates project `verify/demo` (key prefix `vfy`) from a generated config. Ready means `/readyz` is 200 **and** `/api/projects` lists `verify/demo`.
4. Applies `scripts/seed.sql` (verification scaffolding, never run it on a real database): seven issues `vfy-1`..`vfy-7`, three runs with transcripts, a pending human gate on `vfy-6`, a refined spec waiting on `vfy-5`, a succeeded release `v0.1.0` with a pending `production` stage, and a release `fedcba9` waiting at its `production` human gate (a release run with a pending approval, D139).
5. Starts `bun server.ts` in `web/` (serves the UI, proxies `/api` to the control plane) and a headless Chromium with a CDP port.

Everything it starts is detached into its own session, so it survives the shell that launched it. Pids are in `state.env` in the state dir. The checkout is built as-is: uncommitted `internal/` changes are compiled in (the stamped commit is still HEAD), `web/` is served live.

Isolation: ports, container, database and browser profile are per run, so another instance can run beside it with `FRANKIE_VERIFY_RUN=<other id> $S/frankie-verify launch`. A plain second `launch` refuses while a `current` instance exists. Do not drive an instance you did not start in this task (check `frankie-verify env` shows your run id).

## Doctor

```bash
$S/frankie-verify doctor
```

Read-only. Checks that both processes are ours and own their ports, the Postgres container is running, `/api/version` equals the commit it was launched at, `/readyz` is 200, the UI's `/api` proxy reaches the control plane and returns at least 7 seeded issues, the API token is accepted (a 400 on an empty start request, 401 would mean rejected) and the browser answers. Exit 0 and no `FAIL` lines means worth driving. Run it first whenever anything looks off; on a `FAIL`, `cleanup` and `launch` again rather than patching a half-dead instance. It warns (does not fail) when HEAD moved since launch: Go changes then need a relaunch, `web/` changes do not.

`frankie-verify env` reprints the URLs, token and evidence directory. `frankie-verify sql '<query>'` runs a read-only query in the instance's Postgres (use it to verify side effects).

## Drive

Browser commands are `node $S/browser.mjs <cmd>`: a persistent Chromium, each command attaches, acts, detaches, so route, localStorage (API token, "Acting as" name) and page state carry across commands. Run `node $S/browser.mjs` with a bad command to see usage; the header of `scripts/browser.mjs` is the reference.

```
goto <#/route | url>                       #/ runs, #/issues, #/issues/vfy-2, #/approvals, #/releases, #/releases/<id>, #/runs/<id>
click|check|fill|expect|text <locator>     fill takes --value; expect takes --absent --value --enabled --disabled --checked
snapshot --path f.aria.txt | screenshot --path f.png [--full]
locator: --role R --name N | --text T | --label L | --placeholder P   [--in-row TEXT | --in-section TEXT] [--exact] [--nth I]
click --accept-dialog                      for window.confirm (Complete, Abort)
```

Set `FRANKIE_VERIFY_FEATURE=<feature id>` in the shell so each action in `actions.log` is tagged. `expect` exits 1 on failure; every failure message is the reason, report it as is. Default timeout 10 s (`FRANKIE_VERIFY_TIMEOUT_MS`). Prefer roles and accessible names; the UI has few ARIA labels, mostly visible text, and checkboxes labelled `select <key>`.

First thing in a fresh browser, do the two identity bars like an operator does (the UI sends the token and name with every write). Writes fail without the token; without the name they succeed but are recorded as `web`, so the recipes' checks for `verifier` fail:

```bash
export FRANKIE_VERIFY_FEATURE=setup
node $S/browser.mjs goto '#/issues'
node $S/browser.mjs fill --label "Your name" --value verifier
node $S/browser.mjs click --role button --name Save
node $S/browser.mjs click --role button --name "set token"
node $S/browser.mjs fill --label "API token" --value "$API_TOKEN"     # from `frankie-verify env`
node $S/browser.mjs click --role button --name Save
node $S/browser.mjs expect --text "API token set"
```

The feature map is in `features/README.md`: read it, then the file for the feature you are verifying. A proof that drives one entry point is incomplete when the map lists others.

## Evidence

Everything goes under `EVIDENCE_DIR` (`~/.frankie-verify/evidence/<run id>/`), one subfolder per feature id. Cleanup keeps this directory and copies the process logs into `logs/`.

Proof standards:

- Exercise the real operator path: click the buttons, fill the fields. Do not call `/api` writes with curl to stand in for the UI, and do not poke rows into Postgres (the seed is the only SQL write).
- Capture the action and the resulting state: a before snapshot or screenshot, the click, an after snapshot and screenshot. `actions.log` records each browser command with a timestamp.
- Verify side effects beside what is visible: `frankie-verify sql` for the row (e.g. `start_requested_by`) and `curl $API/api/issues/<key>` for what the API returns. A UI success banner alone is not proof.
- Screenshots must show the app identity (the page heading and the "Acting as" bar are in frame).
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
| `scripts/frankie-verify launch \| doctor \| env \| sql '<query>' \| cleanup` | instance lifecycle, shown above |
| `node scripts/browser.mjs start \| status \| stop \| goto \| click \| fill \| check \| expect \| text \| snapshot \| screenshot` | browser driver, shown above (`start`/`stop` are called by launch/cleanup) |
| `scripts/seed.sql` | demo project rows, applied by launch |

Environment overrides: `FRANKIE_VERIFY_STATE`, `FRANKIE_VERIFY_EVIDENCE`, `FRANKIE_VERIFY_RUN`, `FRANKIE_VERIFY_PLAYWRIGHT`, `FRANKIE_VERIFY_TIMEOUT_MS`.

## Other surfaces

- API: the same control plane. `curl $API/api/...`: reads are open (`/api/runs`, `/api/issues?project=verify/demo`, `/api/issues/<key>`, `/api/approvals`, `/api/specs`, `/api/releases?project=verify/demo` (400 without `project`), `/api/version`), writes need `Authorization: Bearer $API_TOKEN` and JSON bodies. Use it for second views, not as a replacement for the UI path.
- `go test ./...` and `bun test` in `web/` (`mise run test`, `mise run lint`) are the code-level checks; this skill is the end-to-end check on top.
- The real factory (port-forward `svc/frankie-web`, token from `mise run api-token`) is for looking, not driving; it is shared and runs real agents.
- `cmd/factoryctl` and the cluster `e2e/` suite need the cluster and are out of scope here.

Maintain the feature map with `/maintain-verification-skill` (`/pstack:maintain-verification-skill` in Claude Code) when routes, labels or seed expectations change.
