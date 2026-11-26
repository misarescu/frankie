# Frankie verification map

The maintained source for verifying what an operator sees in Frankie's web UI. Read this index, then the feature file you need; the recipes use the harness in `../scripts/` and the instance from `../SKILL.md`.

## Baseline preconditions

- `frankie-verify launch` succeeded and `frankie-verify doctor` printed no `FAIL`. Start every feature from a fresh launch unless its file says otherwise: several journeys mutate the seed (a start request, a cancel, a spec approval) and the seed is only applied at launch.
- Seed (scripts/seed.sql), project `verify/demo`, key prefix `vfy`:

  | Key | Type | Status | Why it exists |
  |---|---|---|---|
  | vfy-1 | epic | ready | parent of vfy-2, vfy-3, vfy-5 |
  | vfy-2 | task | ready | startable; vfy-3 waits for it |
  | vfy-3 | task | ready | waits for vfy-2; cancel target |
  | vfy-4 | bug | running | live run `...0204` with steps and a transcript |
  | vfy-5 | story | refining | refined spec waiting for approval, start requested |
  | vfy-6 | task | awaiting_human | pending human gate `approve` (protected paths) |
  | vfy-7 | task | merged | finished run `...0207`, release `v0.1.0` |

- Fixed ids: run `00000000-0000-7000-8000-0000000002NN` belongs to `vfy-NN` (NN = 04, 06, 07); release `00000000-0000-7000-8000-000000000601`.
- Set the "Acting as" name and API token first (the `setup` block in `../SKILL.md`). Reads work without them; every write needs both.
- Set `FRANKIE_VERIFY_FEATURE=<feature id>` and write artifacts to `$EVIDENCE_DIR/<feature id>/` (`frankie-verify env` prints `EVIDENCE_DIR`).
- Shorthand in the recipes: `S=.cursor/skills/verify-frankie/scripts`, `B="node $S/browser.mjs"`.
- There is no Temporal, Kubernetes or GitHub behind this instance: nothing advances on its own, and anything that signals a workflow answers `workflows are not running`.

## Driving conventions

- Navigate with `goto '#/route'` (hash routes). Pages poll every 5-15 s; wait with `expect`, never a fixed sleep.
- Prefer ARIA roles and accessible names; scope to a table row with `--in-row` and to a card with `--in-section`.
- Treat commands as literal; quoted names are the UI's visible text.
- A write is proven by three views: the UI after the action, `frankie-verify sql`, and `curl $API/api/...`.
- Do not remove proof artifacts; `cleanup` keeps `EVIDENCE_DIR`.

## Proof and skip reporting

- UI proof is an ARIA snapshot plus a screenshot taken before and after the action, with the page heading and the "Acting as" bar in frame.
- Record the feature id and entry point with every artifact (`actions.log` does this when `FRANKIE_VERIFY_FEATURE` is set).
- Report an unreachable path with the attempted command and the unmet precondition. Never report a path as verified through a different one.
- Never weaken a journey to make it pass. A failing journey is reported with its reason, and the reason goes in that feature's `Gotchas`.

## Feature entry contract

Each feature file starts with an H1 and one paragraph of user-visible behavior, then exactly four H2s in this order: `Sub-features`, `How to get to it (user POV)`, `Driving it with browser.mjs`, `Gotchas`. Keep implementation details out; name user paths, stable handles, required state, commands and observable proof.

## Features

- [Issues board](./issues-board.md): the project's issues as a tree with dependencies; select and start. Proven end to end (see SKILL.md run).
- [Issue page](./issue-page.md): one issue's spec, relations, runs, and its start, cancel and spec-approval actions.
- [Approvals inbox](./approvals-inbox.md): pending human gates, agent questions and refined specs waiting for a person.
- [Runs and transcripts](./runs.md): the run list and a run's live transcript with its steps table. Currently fails on Chromium 159 (see Gotchas).
- [Releases](./releases.md): a project's releases, their stages, verification, notes and issues.

Not mapped (needs the live factory): human gate decisions taking effect, answering and steering agents, retry, the merge queue, release deployment. Steering/abort panel on a running agent step (`#/runs/<id>`) is mapped in `runs.md` only as far as it renders.
