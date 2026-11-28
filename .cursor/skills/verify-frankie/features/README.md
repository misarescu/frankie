# Frankie verification map

The maintained source for verifying what an operator sees in Frankie's web UI. Read this index, then the feature file you need; the recipes use the harness in `../scripts/` and the instance from `../SKILL.md`.

## Baseline preconditions

- `frankie-verify launch` succeeded and `frankie-verify doctor` printed no `FAIL`. Start every feature from a fresh launch unless its file says otherwise: several journeys mutate the seed (a start request, a cancel, a spec approval) and the seed is only applied at launch.
- Seed (scripts/seed.sql): projects `verify/demo` (key prefix `vfy`) and `verify/other` (`oth`). Ids are fixed: `00000000-0000-7000-8000-<last 12>`, written below as `…<last digits>`.

  | Key | Type, status | Parent | Why it exists |
  |---|---|---|---|
  | vfy-1 | epic, ready | | parent of vfy-2, vfy-3, vfy-5; no cost anywhere (total $0) |
  | vfy-2 | task, ready | vfy-1 | startable; vfy-3 waits for it |
  | vfy-3 | task, ready | vfy-1 | waits for vfy-2; cancel target |
  | vfy-4 | bug, running | | live run `…0204` with steps and a transcript |
  | vfy-5 | story, refining | vfy-1 | refined spec waiting for approval (`/api/specs`), start requested |
  | vfy-6 | task, awaiting_human | | pending human gate `approve` (protected paths), run `…0206` |
  | vfy-7 | task, merged | | finished run `…0207`, in releases `v0.1.0` and `fedcba9` |
  | vfy-8 | epic, ready | | own refinement cost $0.35 (run `…2208`); total **$4.27**; live runs of vfy-11 and vfy-12 under it |
  | vfy-9 | story, ready | vfy-8 | own refinement $0.12 (run `…2209`), spec approved; total **$2.37** (includes canceled vfy-16, D142) |
  | vfy-10 | story, ready | vfy-8 | no own cost; total **$1.55**; waits for vfy-9 |
  | vfy-11 | task, running | vfy-9 | run `…2111`, see below; $1.60; open change request #11; overlaps vfy-12 in `web/src/signup`; changed paths |
  | vfy-12 | task, running | vfy-9 | run `…2112`: agent waiting for an answer (question `…0912` in `/api/questions`); $0.25 |
  | vfy-13 | bug, failed | vfy-10 | run `…2113` failed (aborted by a person); $0.65; the bug issue release `b0b0b0b` opened (`source_ref` = its `bug_issue_ref`) |
  | vfy-14 | task, ready | vfy-10 | 200-character title, 3,200-character spec with code and a 240-character unbroken token, 8 acceptance criteria; waits for vfy-13 and vfy-15; overlaps vfy-11 and vfy-12 |
  | vfy-15 | task, merged | vfy-10 | run `…2115` through every gate type; $0.90; merged change request #15; in releases `b0b0b0b` and `c0ffeec` |
  | vfy-16 | task, canceled | vfy-9 | canceled with a reason after spending $0.40 (run `…2116`) |
  | vfy-17 | task, queued | | in the merge queue: succeeded run `…2117` ending in `merge-queue.enqueue`, open change request #17 |
  | vfy-18 | task, draft | | waits for vfy-11 |
  | oth-1..3 | epic, task ready, task merged | | the second project: oth-2 waits for oth-3; oth-3 has run `…2403` ($0.18) |

  Runs worth opening (`/runs/<id>`):

  | Run | Shows |
  |---|---|
  | `…2111` (vfy-11) | running: `default-build@2` from the repo: `scope`, conditional `plan` skipped (its `when` names `scope`'s `needs_plan`), `implement` attempt 1, `checks` failed and looped back (`loop_counts` 1), `implement` attempt 2, `checks` attempt 2 passed, a parallel review with `review-security` done and `review-style` (pi harness, default model) running, then `review` (agent-verdict), `approve` (human) and `enqueue` (action) not reached. Transcript: state changes, image build and its log, messages, tool calls and results, command output, file diffs, model usage, a question and its answer, a steer delivered and one not delivered, command gate results failing then passing |
  | `…2112` (vfy-12) | a running agent step waiting for an answer (`waits_for: answer`), dependency image build |
  | `…2113` (vfy-13) | `bug-fix@1` failed: findings, conditional human gate `decide` skipped, a must-fail command gate (`expect_fail`), `fix` aborted (abort intervention, error, done failed); later steps not shown since the run ended |
  | `…2115` (vfy-15) | `build-e2e@1` succeeded through preserve, agent-verdict (with a reviewer error), preview, human (approved, `…0415`), provider-checks gates and the `enqueue` action |
  | `…2303`, `…2304` | release agent runs: notes and failure analysis of `b0b0b0b`; notes being written for `c0ffeec` |

  Releases (`/releases/<id>`, project `verify/demo`, newest first):

  | Release | Id | State |
  |---|---|---|
  | `c0ffeec` | `…0604` | in progress: `staging` deploying, release agent running |
  | `fedcba9` | `…0602` | in progress: `staging` passed, waiting at the `production` human gate (D139); release run `…0208` |
  | `b0b0b0b` | `…0603` | rolled back: `staging` verification 12 of 14 with two failed tests (one naming vfy-15), bug issue ref, release agent cost $0.19 |
  | `v0.1.0` | `…0601` | succeeded: `staging` passed, `production` pending |

  The inbox (`/approvals`) lists two gates (`vfy-6` and `release-fedcba9`), one question (vfy-12) and one spec (vfy-5).

- Fixed ids: run `…02NN` belongs to `vfy-NN` for NN = 04, 06, 07 and `…21NN` for NN = 11 to 17; `…22NN` is vfy-NN's refinement; `…0208`, `…2303` and `…2304` are release runs. A step run id ends in `3` + issue number + step number + attempt: `00000000-0000-7000-8000-000000311032` is vfy-11's step 03 (`implement`), attempt 2.
- `frankie-verify api stop` and `api start` take the control plane down and back for the API-error states (see `../SKILL.md`).
- Sign in first via `/login` using `$ADMIN_USER` / `$ADMIN_PASSWORD` from `frankie-verify env` (the `setup` block in `../SKILL.md`). Every `/api/*` read and write requires authentication (D146); writes automatically record the signed-in person (`verifier`).
- For `scripts/walk.mjs`, include `"auth": {"username": "$ADMIN_USER", "password": "$ADMIN_PASSWORD"}` in the paths/steps JSON (use `"auth": null` on a path entry to verify unauthenticated redirect to `/login`).
- Set `FRANKIE_VERIFY_FEATURE=<feature id>` and write artifacts to `$EVIDENCE_DIR/<feature id>/` (`frankie-verify env` prints `EVIDENCE_DIR`).
- Shorthand in the recipes: `S=.cursor/skills/verify-frankie/scripts`, `B="node $S/browser.mjs"`.
- There is no Temporal, Kubernetes or GitHub behind this instance: nothing advances on its own, and anything that signals a workflow answers `workflows are not running`.

## Driving conventions

- Navigate with `goto /path`. From M9 (D141) the UI has path URLs, with tabs, views, filters and the selected step as search parameters: `/` (the last project's board), `/projects/vfy/board` (`?view=graph`, `?finished=1`), `/projects/vfy/runs` (`?status=active|failed`), `/projects/vfy/releases`, `/issues/vfy-11` (`?tab=overview|runs|issues|activity`), `/runs/<id>` (`?step=<step run id>`), `/releases/<id>`, `/approvals` (`?kind=gate|question|spec`); `/projects/oth/board` is the second project. The old hash URLs (`#/issues/vfy-2`) redirect to these. Pages refetch every 3-15 s; wait with `expect`, never a fixed sleep.
- `node ../scripts/walk.mjs` loads a list of these paths in both viewports and themes and fails on console, page or `/api` errors, on a GET `/api` URL asked for more than 4 times in one load, and on text a path says must stay `absent` (also while its `slow` requests are held) or be `present`; a path's `mock` answers chosen `/api` routes with given JSON (a run missing from the run list, `runs.md`); `--history` checks back, forward and reload.
- Prefer ARIA roles and accessible names; scope to a table row with `--in-row` and to a card with `--in-section`.
- Treat commands as literal; quoted names are the UI's visible text.
- A write is proven by three views: the UI after the action, `frankie-verify sql`, and `curl $API/api/...`.
- Do not remove proof artifacts; `cleanup` keeps `EVIDENCE_DIR`.

## Proof and skip reporting

- UI proof is an ARIA snapshot plus a screenshot taken before and after the action, with the page heading and the sidebar (its footer shows your name and `API token set`) in frame.
- Record the feature id and entry point with every artifact (`actions.log` does this when `FRANKIE_VERIFY_FEATURE` is set).
- Report an unreachable path with the attempted command and the unmet precondition. Never report a path as verified through a different one.
- Never weaken a journey to make it pass. A failing journey is reported with its reason, and the reason goes in that feature's `Gotchas`.

## Feature entry contract

Each feature file starts with an H1 and one paragraph of user-visible behavior, then exactly four H2s in this order: `Sub-features`, `How to get to it (user POV)`, `Driving it with browser.mjs`, `Gotchas`. Keep implementation details out; name user paths, stable handles, required state, commands and observable proof.

## Features

- [Sign-in, roles, tokens and users](./auth.md): `/login`, `/invite/<token>`, `/settings/account`, `/settings/users`; session sign-in, personal API tokens, user/invite management and viewer read-only enforcement (M10, D144-D147).
- [Issues board](./issues-board.md): `/projects/<prefix>/board`, the project's issues as a tree or a dependency graph, with rolled-up costs; select and start.
- [Issue page](./issue-page.md): `/issues/<key>`, one issue's header and actions, what waits for a person on it, its live run, and the Overview, Runs, Issues and Activity tabs; start, cancel, retry and spec decisions.
- [Approvals inbox](./approvals-inbox.md): `/approvals`, pending human gates (on issues and before release stages), agent questions and refined specs waiting for a person.
- [Runs and transcripts](./runs.md): `/projects/<prefix>/runs` and `/runs/<id>`, a project's runs with filters, and a run's steps beside the selected step's live transcript, with steer, abort and gate decisions in place.
- [Releases](./releases.md): `/projects/<prefix>/releases` and `/releases/<id>`, a project's releases, their stages, verification, notes and issues.

Not mapped (needs the live factory): human gate decisions taking effect (issue gates and release-stage gates), answering and steering agents, retry, the merge queue, release deployment. The steer and abort controls on a running agent step (`/runs/<id>`) are mapped in `runs.md` only as far as they render and answer `workflows are not running`.
