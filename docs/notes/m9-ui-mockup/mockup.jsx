// Clickable mockup of the M9 UI (D141, D142), agreed with the user on 2026-11-28.
// Example data only; the shapes follow web/src/issues.ts, steps.ts, releases.ts and approvals.ts.
// It is a design reference, not code to copy: the real UI uses shadcn/ui components,
// TanStack Router with path URLs and TanStack Query (see docs/spec/10-roadmap.md, M9).
// index.html is built from this file; see README.md.

const { useState, useEffect, useMemo, useRef, Fragment } = React;

// ---------- example data (shapes follow web/src/issues.ts, steps.ts, releases.ts, approvals.ts) ----------
const PROJECTS = [
  { id: 'gc', name: 'Goodreads-Clone', repo: 'misarescu/Goodreads-Clone', key_prefix: 'gc' },
  { id: 'frk', name: 'frankie', repo: 'misarescu/frankie', key_prefix: 'frk' },
  { id: 'dum', name: 'frankie-dummy', repo: 'misarescu/frankie-dummy', key_prefix: 'dum' },
];

const ISSUES = [
  { key: 'gc-80', project: 'gc', type: 'epic', title: 'Reading lists', status: 'running', cost_usd: 0, depends_on: [], labels: ['factory'] },
  { key: 'gc-81', project: 'gc', parent: 'gc-80', type: 'story', title: 'Shelves: create and organize', status: 'running', cost_usd: 0.03, depends_on: [], labels: ['factory'] },
  { key: 'gc-82', project: 'gc', parent: 'gc-81', type: 'task', title: 'Shelf CRUD endpoints', status: 'merged', cost_usd: 0.41, depends_on: [], change_request: { number: 160, status: 'merged' }, latest_run: { id: 'r82a', status: 'succeeded' }, labels: ['factory'] },
  { key: 'gc-83', project: 'gc', parent: 'gc-81', type: 'task', title: 'Add books to a shelf', status: 'running', cost_usd: 0.27, depends_on: ['gc-82'], change_request: { number: 161, status: 'open' }, latest_run: { id: 'r83b', status: 'running' }, labels: ['factory'] },
  { key: 'gc-84', project: 'gc', parent: 'gc-81', type: 'task', title: 'Shelf page in the web app', status: 'ready', cost_usd: 0, depends_on: ['gc-83'], labels: ['factory'], start_requested_at: '2026-11-28T09:12:00Z' },
  { key: 'gc-85', project: 'gc', parent: 'gc-80', type: 'story', title: 'Reading goals', status: 'running', cost_usd: 0.02, depends_on: [], labels: ['factory'] },
  { key: 'gc-86', project: 'gc', parent: 'gc-85', type: 'task', title: 'Yearly reading goal endpoint', status: 'ready', cost_usd: 0, depends_on: ['gc-82'], labels: ['factory'] },
  { key: 'gc-87', project: 'gc', parent: 'gc-85', type: 'bug', title: 'Goal progress counts re-reads twice', status: 'awaiting_human', cost_usd: 0.12, depends_on: [], latest_run: { id: 'r87a', status: 'running' }, labels: ['factory', 'bug'] },
  { key: 'gc-88', project: 'gc', type: 'story', title: 'Book recommendations', status: 'refining', cost_usd: 0.04, depends_on: [], labels: ['factory', 'needs-spec-approval'] },
  { key: 'gc-89', project: 'gc', parent: 'gc-88', type: 'task', title: 'Similar books by shared tags', status: 'draft', cost_usd: 0, depends_on: [], labels: ['factory'] },
  { key: 'gc-78', project: 'gc', type: 'task', title: 'GET /api/users/{id}/tags', status: 'merged', cost_usd: 0.46, depends_on: [], change_request: { number: 156, status: 'merged' }, latest_run: { id: 'r78a', status: 'succeeded' }, labels: ['factory'] },
  { key: 'gc-79', project: 'gc', type: 'task', title: 'Sort user tags alphabetically', status: 'canceled', cost_usd: 0, depends_on: [], labels: ['factory'] },
  { key: 'frk-7', project: 'frk', type: 'task', title: 'Report the build date in /api/version', status: 'awaiting_human', cost_usd: 0.09, depends_on: [], latest_run: { id: 'r7a', status: 'running' }, change_request: { number: 177, status: 'open' }, labels: ['factory'] },
  { key: 'frk-5', project: 'frk', type: 'task', title: 'Version endpoint reports the commit', status: 'merged', cost_usd: 0.38, depends_on: [], change_request: { number: 168, status: 'merged' }, latest_run: { id: 'r5a', status: 'succeeded' }, labels: ['factory'] },
  { key: 'dum-1', project: 'dum', type: 'task', title: 'Build the todo service', status: 'merged', cost_usd: 0.13, depends_on: [], change_request: { number: 2, status: 'merged' }, latest_run: { id: 'rd1', status: 'succeeded' }, labels: ['factory'] },
];
const byKey = Object.fromEntries(ISSUES.map(i => [i.key, i]));
const childrenOf = (k) => ISSUES.filter(i => i.parent === k);
const totalCost = (k) => byKey[k].cost_usd + childrenOf(k).reduce((s, c) => s + totalCost(c.key), 0);
const leaves = (k) => { const c = childrenOf(k); return c.length ? c.flatMap(x => leaves(x.key)) : [byKey[k]]; };
const ancestry = (k) => { const out = []; let i = byKey[k]; while (i && i.parent) { i = byKey[i.parent]; out.unshift(i); } return out; };
const dependents = (k) => ISSUES.filter(i => i.depends_on.includes(k));

const DETAIL = {
  'gc-83': {
    spec: 'A signed-in reader adds a book to one of their shelves and removes it again. A book sits on a shelf at most once; adding it twice is a no-op that still answers 200. Shelves belong to one reader: another reader gets 404, never 403, so shelf ids do not leak.\n\nThe endpoints follow the shelf CRUD conventions from gc-82 (JSON bodies, problem+json errors).',
    acceptance_criteria: ['POST /api/shelves/{id}/books with {"bookId"} adds the book and answers 200 with the shelf', 'Adding a book already on the shelf answers 200 and leaves one entry', 'DELETE /api/shelves/{id}/books/{bookId} removes it and answers 204', "Another reader's shelf answers 404", 'GET /api/shelves/{id} lists the books in the order they were added'],
    created_by: 'github-sync', source_ref: 'misarescu/Goodreads-Clone#162', priority: 2,
    changed_paths: ['internal/shelves/handler.go', 'internal/shelves/store.go', 'internal/shelves/handler_test.go', 'db/migrations/20261128_shelf_books.sql'],
    overlaps: [{ with: 'gc-86', paths: ['db/migrations/'] }],
    usage: { input_tokens: 412000, output_tokens: 18400 },
    runs: [{ id: 'r83b', n: 2, status: 'running', workflow: 'build-e2e', created_at: '11:02', cost_usd: 0.21 }, { id: 'r83a', n: 1, status: 'failed', workflow: 'build-e2e', created_at: '10:31', cost_usd: 0.06 }],
    activity: [
      { t: '10:30', who: 'github-sync', what: 'synced from misarescu/Goodreads-Clone#162' },
      { t: '10:31', who: 'You', what: 'asked to start gc-81 (3 issues) from the board' },
      { t: '10:31', who: 'scheduler', what: 'started Run 1 (build-e2e)' },
      { t: '10:52', who: 'Run 1', what: 'failed: the toolchain image build timed out (registry unreachable)' },
      { t: '11:02', who: 'You', what: 'retried the issue' },
      { t: '11:19', who: 'Run 2', what: 'checks failed: 2 tests in internal/shelves; back to implement (loop 1 of 3)' },
      { t: '11:31', who: 'Run 2', what: 'opened change request #161' },
      { t: '11:34', who: 'Run 2', what: 'review started (reviewer, read-only)' },
    ],
  },
};
const detailFor = (k) => DETAIL[k] || {
  spec: byKey[k].type === 'epic' ? 'Readers keep books on shelves and set reading goals. This epic covers the API and the web app.' : 'Spec synced from the GitHub issue.',
  acceptance_criteria: byKey[k].type === 'epic' ? [] : ['Behaviour described in the spec is covered by tests'],
  created_by: 'github-sync', source_ref: 'misarescu/' + (PROJECTS.find(p => p.id === byKey[k].project).name) + '#' + (100 + parseInt(k.split('-')[1])), priority: 3,
  changed_paths: [], overlaps: [], usage: { input_tokens: Math.round(byKey[k].cost_usd * 1.4e6), output_tokens: Math.round(byKey[k].cost_usd * 6e4) },
  runs: byKey[k].latest_run ? [{ id: byKey[k].latest_run.id, n: 1, status: byKey[k].latest_run.status, workflow: byKey[k].type === 'bug' ? 'bug-fix' : 'default-build', created_at: '09:40', cost_usd: byKey[k].cost_usd }] : [],
  activity: [{ t: '09:00', who: 'github-sync', what: 'synced from GitHub' }],
};

// Run 2 of gc-83: build-e2e with a failed checks gate that looped back to implement.
const RUNS = {
  r83b: { id: 'r83b', n: 2, issue: 'gc-83', workflow: 'build-e2e', status: 'running', started: '11:02', duration: '34m', cost_usd: 0.21, current: 'review',
    steps: [
      { id: 's1', step_id: 'implement', attempt: 1, kind: 'agent', status: 'succeeded', role: 'coder', harness: 'claude-code', model: 'anthropic/claude-sonnet-5-5', cost_usd: 0.09, dur: '7m 12s', commit_sha: '4e1c9a0' },
      { id: 's2', step_id: 'e2e-tests', attempt: 1, kind: 'agent', status: 'succeeded', role: 'e2e-writer', harness: 'claude-code', model: 'anthropic/claude-sonnet-5-5', cost_usd: 0.04, dur: '5m 40s', parallel: true, commit_sha: 'b20f7d3' },
      { id: 's3', step_id: 'checks', attempt: 1, kind: 'gate', gate: 'command', status: 'failed', cost_usd: 0, dur: '2m 03s', commit_sha: '4e1c9a0', loop: 'back to implement, loop 1 of 3' },
      { id: 's4', step_id: 'implement', attempt: 2, kind: 'agent', status: 'succeeded', role: 'coder', harness: 'claude-code', model: 'anthropic/claude-sonnet-5-5', cost_usd: 0.05, dur: '4m 18s', commit_sha: '9d03e5b' },
      { id: 's5', step_id: 'checks', attempt: 2, kind: 'gate', gate: 'command', status: 'succeeded', cost_usd: 0, dur: '1m 58s', commit_sha: '9d03e5b' },
      { id: 's6', step_id: 'e2e', attempt: 1, kind: 'gate', gate: 'command', status: 'succeeded', cost_usd: 0, dur: '3m 11s', commit_sha: '9d03e5b' },
      { id: 's7', step_id: 'review', attempt: 1, kind: 'gate', gate: 'agent-verdict', status: 'running', role: 'reviewer', harness: 'opencode', model: 'openrouter/qwen/qwen3-coder', cost_usd: 0.03, dur: '2m 40s', commit_sha: '9d03e5b' },
      { id: 's8', step_id: 'preview', attempt: 1, kind: 'gate', gate: 'preview', status: 'pending' },
      { id: 's9', step_id: 'merge-approval', attempt: 1, kind: 'gate', gate: 'human', status: 'skipped', skipped_because: 'mergeApproval is not required' },
      { id: 's10', step_id: 'enqueue', attempt: 1, kind: 'merge-queue.enqueue', status: 'pending' },
    ] },
};
RUNS.r87a = { id: 'r87a', n: 1, issue: 'gc-87', workflow: 'bug-fix', status: 'awaiting_human', started: '10:44', duration: '52m', cost_usd: 0.12, current: 'decide',
  steps: [
    { id: 'b1', step_id: 'investigate', attempt: 1, kind: 'agent', status: 'succeeded', role: 'investigator', harness: 'claude-code', model: 'anthropic/claude-sonnet-5-5', cost_usd: 0.12, dur: '6m 30s' },
    { id: 'b2', step_id: 'decide', attempt: 1, kind: 'gate', gate: 'human', status: 'awaiting_human', dur: '25m' },
    { id: 'b3', step_id: 'reproduce', attempt: 1, kind: 'agent', status: 'pending', role: 'coder', harness: 'claude-code', model: 'anthropic/claude-sonnet-5-5' },
    { id: 'b4', step_id: 'must-fail', attempt: 1, kind: 'gate', gate: 'command', status: 'pending' },
    { id: 'b5', step_id: 'fix', attempt: 1, kind: 'agent', status: 'pending', role: 'coder', harness: 'claude-code', model: 'anthropic/claude-sonnet-5-5' },
    { id: 'b6', step_id: 'checks', attempt: 1, kind: 'gate', gate: 'command', status: 'pending' },
    { id: 'b7', step_id: 'enqueue', attempt: 1, kind: 'merge-queue.enqueue', status: 'pending' } ] };
RUNS.r7a = { id: 'r7a', n: 1, issue: 'frk-7', workflow: 'default-build', status: 'awaiting_human', started: '10:20', duration: '1h 16m', cost_usd: 0.09, current: 'protected-changes',
  steps: [
    { id: 'c1', step_id: 'implement', attempt: 1, kind: 'agent', status: 'succeeded', role: 'coder', harness: 'claude-code', model: 'anthropic/claude-sonnet-5-5', cost_usd: 0.08, dur: '9m 12s', commit_sha: 'c81e2f9' },
    { id: 'c2', step_id: 'checks', attempt: 1, kind: 'gate', gate: 'command', status: 'succeeded', dur: '4m 40s', commit_sha: 'c81e2f9' },
    { id: 'c3', step_id: 'review', attempt: 1, kind: 'gate', gate: 'agent-verdict', status: 'succeeded', role: 'reviewer', harness: 'opencode', model: 'openrouter/qwen/qwen3-coder', cost_usd: 0.01, dur: '1m 05s', commit_sha: 'c81e2f9' },
    { id: 'c4', step_id: 'protected-changes', attempt: 1, kind: 'gate', gate: 'human', status: 'awaiting_human', dur: '1h', commit_sha: 'c81e2f9' },
    { id: 'c5', step_id: 'preview', attempt: 1, kind: 'gate', gate: 'preview', status: 'pending' },
    { id: 'c6', step_id: 'enqueue', attempt: 1, kind: 'merge-queue.enqueue', status: 'pending' } ] };
const runFor = (id) => RUNS[id] || { id, n: 1, issue: Object.values(byKey).find(i => i.latest_run && i.latest_run.id === id)?.key || 'gc-82', workflow: 'default-build', status: 'succeeded', started: '09:40', duration: '16m', cost_usd: 0.41, current: null,
  steps: [
    { id: 'a1', step_id: 'implement', attempt: 1, kind: 'agent', status: 'succeeded', role: 'coder', harness: 'claude-code', model: 'anthropic/claude-sonnet-5-5', cost_usd: 0.36, dur: '8m 02s', commit_sha: '71aa2c4' },
    { id: 'a2', step_id: 'checks', attempt: 1, kind: 'gate', gate: 'command', status: 'succeeded', dur: '2m 10s', commit_sha: '71aa2c4' },
    { id: 'a3', step_id: 'review', attempt: 1, kind: 'gate', gate: 'agent-verdict', status: 'succeeded', role: 'reviewer', harness: 'opencode', model: 'openrouter/qwen/qwen3-coder', cost_usd: 0.05, dur: '1m 12s', commit_sha: '71aa2c4' },
    { id: 'a4', step_id: 'enqueue', attempt: 1, kind: 'merge-queue.enqueue', status: 'succeeded', dur: '3m 30s' },
  ] };

const TRANSCRIPTS = {
  s3: [
    { type: 'gate', title: 'mise run lint', status: 'succeeded', body: 'golangci-lint run ./...\n0 issues.' },
    { type: 'gate', title: 'mise run test', status: 'failed', body: '--- FAIL: TestAddBook/duplicate_is_a_no-op (0.01s)\n    handler_test.go:88: shelf has 2 entries for book 17, want 1\n--- FAIL: TestRemoveBook/other_reader (0.00s)\n    handler_test.go:131: status 403, want 404\nFAIL\tgithub.com/misarescu/Goodreads-Clone/internal/shelves\t0.412s' },
  ],
  s4: [
    { type: 'user', body: 'The checks gate failed on 4e1c9a0. Fix the failures below and push.\n\nTestAddBook/duplicate_is_a_no-op: shelf has 2 entries for book 17, want 1\nTestRemoveBook/other_reader: status 403, want 404' },
    { type: 'assistant', body: "Two problems: the insert has no uniqueness guard, and the ownership check returns 403. I'll add a unique index and ON CONFLICT DO NOTHING, and answer 404 for shelves the reader doesn't own." },
    { type: 'tool', name: 'Read', body: 'internal/shelves/store.go' },
    { type: 'tool', name: 'Edit', body: 'internal/shelves/store.go  +3 -1\n  INSERT INTO shelf_books (shelf_id, book_id) VALUES ($1, $2)\n+ ON CONFLICT (shelf_id, book_id) DO NOTHING' },
    { type: 'tool', name: 'Edit', body: 'internal/shelves/handler.go  +2 -2\n- http.Error(w, "forbidden", http.StatusForbidden)\n+ writeProblem(w, http.StatusNotFound, "shelf not found")' },
    { type: 'tool', name: 'Bash', body: '$ mise run test\nok  \tgithub.com/misarescu/Goodreads-Clone/internal/shelves\t0.398s\nok  \tgithub.com/misarescu/Goodreads-Clone/internal/books\t0.211s' },
    { type: 'tool', name: 'Bash', body: '$ git push origin agent/gc-83/add-books-to-shelf\n   4e1c9a0..9d03e5b' },
    { type: 'result', body: 'Pushed 9d03e5b: unique (shelf_id, book_id), ON CONFLICT DO NOTHING, 404 for shelves of other readers.' },
  ],
  s7: [
    { type: 'user', body: 'Review change request #161 at 9d03e5b against the spec and acceptance criteria. Reply with the review-verdict schema.' },
    { type: 'tool', name: 'Bash', body: '$ git diff main...9d03e5b --stat\n internal/shelves/handler.go      | 64 ++++++++\n internal/shelves/store.go        | 41 +++++\n internal/shelves/handler_test.go | 118 ++++++++++++\n db/migrations/20261128_shelf_books.sql | 9 +\n 4 files changed, 232 insertions(+)' },
    { type: 'tool', name: 'Read', body: 'internal/shelves/handler.go' },
    { type: 'assistant', body: 'Criteria 1-4 are covered by handler_test.go. Criterion 5 (order of addition) relies on the id sequence; checking whether the list query orders by added_at...', live: true },
  ],
};
const transcriptFor = (s) => TRANSCRIPTS[s.id] || (s.kind === 'gate' && s.gate === 'command' ? [{ type: 'gate', title: 'mise run lint', status: 'succeeded', body: '0 issues.' }, { type: 'gate', title: 'mise run test', status: s.status === 'failed' ? 'failed' : 'succeeded', body: 'ok  \t./...' }]
  : s.status === 'awaiting_human' ? [{ type: 'result', body: 'Waiting for a person in Approvals.' }] : s.status === 'pending' ? [] : s.status === 'skipped' ? [] : [{ type: 'result', body: s.kind === 'agent' ? 'Pushed ' + (s.commit_sha || '') + '.' : 'Verdict: approve. No findings.' }]);

const APPROVALS = [
  { id: 'ap1', kind: 'gate', title: 'Decide how to fix gc-87', issue: 'gc-87', run: 'r87a', step: 'decide', created: '25m ago', expires: 'in 6 days',
    reason: 'The investigation found the cause: progress counts reading_events, and a re-read writes a second "finished" event. No test can show it without a fixture of real re-reads, so the bug-fix workflow asks you before writing the fix.', actions: ['approve', 'request-changes', 'reject'] },
  { id: 'ap2', kind: 'protected', title: 'Protected paths changed in frk-7', issue: 'frk-7', run: 'r7a', step: 'protected-changes', created: '1h ago', expires: 'in 6 days', cr: 177, commit: 'c81e2f9',
    reason: 'The change touches deploy/helm/frankie/templates/networkpolicy-controlplane.yaml (NetworkPolicies are protected, invariant 6).', paths: ['deploy/helm/frankie/templates/networkpolicy-controlplane.yaml'], actions: ['approve', 'request-changes', 'reject'] },
  { id: 'ap3', kind: 'question', title: 'The coder of frk-7 asks', issue: 'frk-7', run: 'r7a', step: 'implement', created: '12m ago', expires: 'in 20h',
    reason: 'Should /api/version report the build date in UTC RFC 3339, or as the Unix time the image was built? The UI only shows it as text today.' },
  { id: 'ap4', kind: 'spec', title: 'Approve the refined spec of gc-88', issue: 'gc-88', created: '2h ago',
    reason: 'The refiner split the story into one task (gc-89) and added two acceptance criteria about readers with no tags.' },
  { id: 'ap5', kind: 'release', title: 'Deploy v0.1.2 to production', release: 'rel-012', created: '40m ago', expires: 'in 3 days',
    reason: 'Goodreads-Clone v0.1.2 passed staging: 381 of 381 e2e tests. It contains gc-82 and gc-78.', actions: ['approve', 'reject'] },
];

const RELEASES = [
  { id: 'rel-012', version: 'v0.1.2', bump: 'patch', sha: 'f3b91d2', status: 'in_progress', created: 'Today 10:58', issues: ['gc-82', 'gc-78'], cost_usd: 0.04,
    stages: [{ stage: 'staging', status: 'succeeded' }, { stage: 'production', status: 'awaiting_human' }],
    verification: { staging: { passed: true, summary: '381 of 381 tests passed' } },
    notes: '### Added\n- Shelves: create, rename and delete shelves (gc-82).\n- GET /api/users/{id}/tags lists the tags a reader uses (gc-78).' },
  { id: 'rel-011', version: 'v0.1.1', bump: 'patch', sha: '6ea0c02', status: 'succeeded', created: 'Nov 27 07:41', issues: ['gc-78'], cost_usd: 0.03,
    stages: [{ stage: 'staging', status: 'succeeded' }, { stage: 'production', status: 'succeeded' }], verification: { staging: { passed: true, summary: '369 of 369 tests passed' } }, notes: '### Fixed\n- Tag lists no longer repeat tags.' },
  { id: 'rel-rb', version: null, sha: 'dc515ab', status: 'rolled_back', created: 'Nov 17 21:02', issues: [], cost_usd: 0.02, bug: 'misarescu/Goodreads-Clone#158',
    stages: [{ stage: 'staging', status: 'rolled_back' }, { stage: 'production', status: 'pending' }],
    verification: { staging: { passed: false, summary: '362 of 369 tests passed', failures: ['user tags are sorted by first use', 'tags of a reader with no books'] } } },
  { id: 'rel-010', version: 'v0.1.0', bump: 'minor', sha: 'abb5e70', status: 'succeeded', created: 'Nov 17 20:21', issues: ['gc-78'], cost_usd: 0.03,
    stages: [{ stage: 'staging', status: 'succeeded' }], verification: { staging: { passed: true, summary: '369 of 369 tests passed' } }, notes: '### Added\n- First release.' },
];

const ALL_RUNS = [
  { id: 'r83b', n: 2, issue: 'gc-83', workflow: 'build-e2e', status: 'running', step: 'review', started: '11:02', duration: '34m', cost: 0.21 },
  { id: 'r87a', n: 1, issue: 'gc-87', workflow: 'bug-fix', status: 'awaiting_human', step: 'decide', started: '10:44', duration: '52m', cost: 0.12 },
  { id: 'r7a', n: 1, issue: 'frk-7', workflow: 'default-build', status: 'awaiting_human', step: 'protected-changes', started: '10:20', duration: '1h 16m', cost: 0.09 },
  { id: 'rrel012', n: 1, issue: null, release: 'v0.1.2', workflow: 'release-agent', status: 'succeeded', step: 'notes', started: '10:58', duration: '1m', cost: 0.04 },
  { id: 'r83a', n: 1, issue: 'gc-83', workflow: 'build-e2e', status: 'failed', step: 'implement', started: '10:31', duration: '21m', cost: 0.06 },
  { id: 'r82a', n: 1, issue: 'gc-82', workflow: 'build-e2e', status: 'succeeded', step: null, started: '09:40', duration: '42m', cost: 0.41 },
  { id: 'r5a', n: 1, issue: 'frk-5', workflow: 'default-build', status: 'succeeded', step: null, started: 'Nov 27', duration: '1h 02m', cost: 0.38 },
  { id: 'rd1', n: 1, issue: 'dum-1', workflow: 'default-build', status: 'succeeded', step: null, started: 'Nov 19', duration: '16m', cost: 0.13 },
];

// ---------- primitives (shadcn-style) ----------
const ICONS = {
  board: '<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M8 7v7"/><path d="M12 7v4"/><path d="M16 7v9"/>',
  runs: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
  inbox: '<polyline points="22 12 16 12 14 15 10 15 8 12 2 12"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>',
  rocket: '<path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z"/><path d="m12 15-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z"/><path d="M9 12H4s.55-3.03 2-4c1.62-1.08 5 0 5 0"/><path d="M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5"/>',
  settings: '<line x1="21" x2="14" y1="4" y2="4"/><line x1="10" x2="3" y1="4" y2="4"/><line x1="21" x2="12" y1="12" y2="12"/><line x1="8" x2="3" y1="12" y2="12"/><line x1="21" x2="16" y1="20" y2="20"/><line x1="12" x2="3" y1="20" y2="20"/><line x1="14" x2="14" y1="2" y2="6"/><line x1="8" x2="8" y1="10" y2="14"/><line x1="16" x2="16" y1="18" y2="22"/>',
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  right: '<path d="m9 18 6-6-6-6"/>', down: '<path d="m6 9 6 6 6-6"/>', updown: '<path d="m7 15 5 5 5-5"/><path d="m7 9 5-5 5 5"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/><path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/><path d="M2 12h2"/><path d="M20 12h2"/><path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>',
  moon: '<path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>',
  pr: '<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M13 6h3a2 2 0 0 1 2 2v7"/><line x1="6" x2="6" y1="9" y2="21"/>',
  commit: '<circle cx="12" cy="12" r="3"/><line x1="3" x2="9" y1="12" y2="12"/><line x1="15" x2="21" y1="12" y2="12"/>',
  play: '<polygon points="6 3 20 12 6 21 6 3"/>', retry: '<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/>',
  x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>', check: '<path d="M20 6 9 17l-5-5"/>',
  menu: '<line x1="4" x2="20" y1="12" y2="12"/><line x1="4" x2="20" y1="6" y2="6"/><line x1="4" x2="20" y1="18" y2="18"/>',
  question: '<path d="M7.9 20A9 9 0 1 0 4 16.1L2 22Z"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><path d="M12 17h.01"/>',
  shield: '<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/>',
  doc: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M16 13H8"/><path d="M16 17H8"/>',
  terminal: '<polyline points="4 17 10 11 4 5"/><line x1="12" x2="20" y1="19" y2="19"/>',
  bot: '<path d="M12 8V4H8"/><rect width="16" height="12" x="4" y="8" rx="2"/><path d="M2 14h2"/><path d="M20 14h2"/><path d="M15 13v2"/><path d="M9 13v2"/>',
  user: '<path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',
  alert: '<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>',
  ext: '<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>',
  list: '<line x1="8" x2="21" y1="6" y2="6"/><line x1="8" x2="21" y1="12" y2="12"/><line x1="8" x2="21" y1="18" y2="18"/><line x1="3" x2="3.01" y1="6" y2="6"/><line x1="3" x2="3.01" y1="12" y2="12"/><line x1="3" x2="3.01" y1="18" y2="18"/>',
  graph: '<rect x="16" y="16" width="6" height="6" rx="1"/><rect x="2" y="16" width="6" height="6" rx="1"/><rect x="9" y="2" width="6" height="6" rx="1"/><path d="M5 16v-3a1 1 0 0 1 1-1h12a1 1 0 0 1 1 1v3"/><path d="M12 12V8"/>',
  stop: '<rect width="14" height="14" x="5" y="5" rx="1"/>', send: '<path d="m22 2-7 20-4-9-9-4Z"/><path d="M22 2 11 13"/>',
  epic: '<path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"/><path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65"/><path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65"/>',
  story: '<path d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H20v20H6.5a2.5 2.5 0 0 1 0-5H20"/>',
  task: '<rect width="18" height="18" x="3" y="3" rx="2"/><path d="m9 12 2 2 4-4"/>',
  bug: '<path d="m8 2 1.88 1.88"/><path d="M14.12 3.88 16 2"/><path d="M9 7.13v-1a3.003 3.003 0 1 1 6 0v1"/><path d="M12 20c-3.3 0-6-2.7-6-6v-3a4 4 0 0 1 4-4h4a4 4 0 0 1 4 4v3c0 3.3-2.7 6-6 6"/><path d="M12 20v-9"/><path d="M6.53 9C4.6 8.8 3 7.1 3 5"/><path d="M6 13H2"/><path d="M3 21c0-2.1 1.7-3.9 3.8-4"/><path d="M20.97 5c0 2.1-1.6 3.8-3.5 4"/><path d="M22 13h-4"/><path d="M17.2 17c2.1.1 3.8 1.9 3.8 4"/>',
  gate: '<path d="M12 22c5.523 0 10-4.477 10-10S17.523 2 12 2 2 6.477 2 12s4.477 10 10 10z"/><path d="m9 12 2 2 4-4"/>',
  queue: '<path d="M3 12h18"/><path d="M3 6h18"/><path d="M3 18h12"/>',
  branch: '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
  loop: '<path d="m17 2 4 4-4 4"/><path d="M3 11v-1a4 4 0 0 1 4-4h14"/><path d="m7 22-4-4 4-4"/><path d="M21 13v1a4 4 0 0 1-4 4H3"/>',
};
function Icon({ name, className = '' }) {
  return <svg className={/(^|\s)h-/.test(className) ? className : 'h-4 w-4 shrink-0 ' + className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" dangerouslySetInnerHTML={{ __html: ICONS[name] || '' }} />;
}
const cx = (...a) => a.filter(Boolean).join(' ');
const usd = (n) => '$' + n.toFixed(2);

function Button({ variant = 'default', size = 'md', className, children, ...p }) {
  const v = { default: 'bg-primary text-primary-fg hover:opacity-90', outline: 'border border-line bg-card hover:bg-hover', ghost: 'hover:bg-hover',
    destructive: 'border border-line bg-card text-bad hover:bg-bad-soft', soft: 'bg-primary-soft text-primary hover:opacity-90' }[variant];
  const s = { sm: 'h-7 px-2.5 text-xs gap-1.5', md: 'h-8 px-3 text-[13px] gap-2', icon: 'h-8 w-8 justify-center' }[size];
  return <button className={cx('inline-flex items-center rounded-md font-medium transition-colors disabled:opacity-50 disabled:pointer-events-none whitespace-nowrap', v, s, className)} {...p}>{children}</button>;
}

const TONE = {
  draft: 'neutral', refining: 'primary', ready: 'outline', running: 'primary', queued: 'primary', building_image: 'primary', in_progress: 'primary',
  awaiting_human: 'warn', merged: 'ok', succeeded: 'ok', released: 'ok', failed: 'bad', rolled_back: 'bad', canceled: 'neutral', pending: 'outline', skipped: 'outline', open: 'primary', inconclusive: 'warn',
};
const LABEL = { awaiting_human: 'waiting for you', rolled_back: 'rolled back', in_progress: 'in progress', building_image: 'building image' };
function Status({ s, className }) {
  const t = TONE[s] || 'neutral';
  const cls = { neutral: 'bg-muted text-muted-fg', outline: 'border border-line text-muted-fg', primary: 'bg-primary-soft text-primary', warn: 'bg-warn-soft text-warn', ok: 'bg-ok-soft text-ok', bad: 'bg-bad-soft text-bad' }[t];
  const live = s === 'running' || s === 'refining' || s === 'in_progress';
  return <span className={cx('inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs2 font-medium whitespace-nowrap', cls, s === 'canceled' && 'line-through', className)}>
    <span className={cx('h-1.5 w-1.5 rounded-full bg-current', live && 'pulse', (t === 'outline') && 'opacity-60')} />{LABEL[s] || s.replace('_', ' ')}
  </span>;
}
function TypeIcon({ type, className = 'h-3.5 w-3.5' }) {
  const color = { epic: 'text-primary', story: 'text-ok', task: 'text-muted-fg', bug: 'text-bad' }[type];
  return <span className={color} title={type}><Icon name={type} className={className} /></span>;
}
function Card({ className, children }) { return <div className={cx('rounded-lg border border-line bg-card', className)}>{children}</div>; }
function CardHead({ title, right, sub }) {
  return <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-2.5"><div className="min-w-0"><h3 className="text-[13px] font-semibold">{title}</h3>{sub && <p className="text-xs text-muted-fg">{sub}</p>}</div>{right}</div>;
}
function Kbd({ children }) { return <kbd className="mono rounded border border-line bg-muted px-1.5 text-[10px] text-muted-fg">{children}</kbd>; }
function Progress({ k }) {
  const l = leaves(k); const done = l.filter(x => x.status === 'merged' || x.status === 'canceled').length;
  const pct = Math.round(100 * done / l.length);
  return <div className="flex items-center gap-2" title={done + ' of ' + l.length + ' done'}><div className="h-1.5 w-16 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-ok" style={{ width: pct + '%' }} /></div><span className="tnum text-xs2 text-muted-fg">{done}/{l.length}</span></div>;
}
function IssueLink({ k, nav, showTitle }) {
  const i = byKey[k];
  return <a href={'#issue-' + k} onClick={(e) => { e.preventDefault(); nav({ page: 'issue', key: k }); }} className="mono text-[12.5px] text-primary hover:underline">{k}{showTitle && <span className="font-sans text-fg"> {i.title}</span>}</a>;
}
function Tabs({ tabs, value, onChange }) {
  return <div className="flex gap-1 border-b border-line" role="tablist">{tabs.map(t => (
    <button key={t.id} role="tab" aria-selected={value === t.id} onClick={() => onChange(t.id)}
      className={cx('-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-[13px] font-medium transition-colors', value === t.id ? 'border-primary text-fg' : 'border-transparent text-muted-fg hover:text-fg')}>
      {t.label}{t.count != null && <span className="tnum rounded-full bg-muted px-1.5 text-xs2 text-muted-fg">{t.count}</span>}</button>))}</div>;
}
function Segmented({ options, value, onChange }) {
  return <div className="inline-flex rounded-md border border-line bg-muted p-0.5">{options.map(o => (
    <button key={o.id} onClick={() => onChange(o.id)} className={cx('flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium', value === o.id ? 'bg-card text-fg shadow-sm' : 'text-muted-fg hover:text-fg')}>
      {o.icon && <Icon name={o.icon} className="h-3.5 w-3.5" />}{o.label}</button>))}</div>;
}
function Toast({ msg }) { return msg ? <div className="fixed bottom-4 right-4 z-50 rounded-md border border-line bg-card px-4 py-2.5 text-[13px] shadow-lg" style={{ marginBottom: 'env(safe-area-inset-bottom, 0px)' }}>{msg}</div> : null; }
function PageHead({ title, sub, right, icon }) {
  return <div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><h1 className="flex items-center gap-2 text-xl font-semibold tracking-tight">{icon}{title}</h1>{sub && <p className="mt-0.5 text-[13px] text-muted-fg">{sub}</p>}</div>{right && <div className="flex flex-wrap items-center gap-2">{right}</div>}</div>;
}
function Stat({ label, value, sub, tone }) {
  return <div className="min-w-0 px-4 py-3"><div className="text-xs2 font-medium uppercase tracking-wider text-muted-fg">{label}</div>
    <div className={cx('tnum mt-0.5 text-lg font-semibold', tone === 'warn' && 'text-warn', tone === 'primary' && 'text-primary')}>{value}</div>{sub && <div className="truncate text-xs text-muted-fg">{sub}</div>}</div>;
}

// ---------- Board ----------
function Board({ project, nav, toast }) {
  const [view, setView] = useState('list');
  const [showFinished, setShowFinished] = useState(false);
  const [collapsed, setCollapsed] = useState({});
  const [sel, setSel] = useState(new Set(['gc-84', 'gc-86']));
  const fin = (s) => s === 'merged' || s === 'canceled';
  const roots = ISSUES.filter(i => i.project === project && !i.parent);
  const rows = [];
  const walk = (i, d) => { const kids = childrenOf(i.key); const allFin = fin(i.status) && kids.every(k => fin(k.status));
    if (!showFinished && allFin) return; rows.push({ i, d, kids: kids.length });
    if (!collapsed[i.key]) kids.forEach(k => walk(k, d + 1)); };
  roots.forEach(r => walk(r, 0));
  const inProject = ISSUES.filter(i => i.project === project);
  const running = inProject.filter(i => i.status === 'running' && !childrenOf(i.key).length).length;
  const waiting = inProject.filter(i => i.status === 'awaiting_human').length + (project === 'gc' ? 2 : 0);
  const spent = inProject.filter(i => !i.parent).reduce((s, i) => s + totalCost(i.key), 0);
  const toggle = (k) => { const n = new Set(sel); n.has(k) ? n.delete(k) : n.add(k); setSel(n); };
  return <div className="space-y-5">
    <PageHead title="Board" sub={PROJECTS.find(p => p.id === project).repo + ' · issues labeled factory'}
      right={<><Segmented value={view} onChange={setView} options={[{ id: 'list', label: 'List', icon: 'list' }, { id: 'graph', label: 'Dependencies', icon: 'graph' }]} />
        <Button disabled={!sel.size} onClick={() => { toast('Asked to start ' + [...sel].join(', ') + '. gc-84 waits for gc-83.'); setSel(new Set()); }}><Icon name="play" className="h-3.5 w-3.5" />Start {sel.size || ''} selected</Button></>} />
    <Card className="grid grid-cols-2 divide-line sm:grid-cols-4 sm:divide-x">
      <Stat label="Running" value={running} sub="agent steps and gates" tone="primary" />
      <Stat label="Waiting for you" value={waiting} sub="gates, questions, specs" tone={waiting ? 'warn' : undefined} />
      <Stat label="Merged this week" value={inProject.filter(i => i.status === 'merged').length} sub="through the merge queue" />
      <Stat label="Spent" value={usd(spent)} sub="all issues, rolled up" />
    </Card>
    {view === 'list' ? <Card className="overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-3 py-2">
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted-fg"><span>{rows.length} issues</span><span>·</span><span>epics and stories show the cost of everything under them</span></div>
        <label className="flex items-center gap-2 text-xs text-muted-fg"><input id="show-finished" type="checkbox" className="accent-[var(--primary)]" checked={showFinished} onChange={e => setShowFinished(e.target.checked)} />Show finished</label>
      </div>
      <div className="overflow-x-auto scroll-thin"><table className="w-full min-w-[760px] text-[13px]">
        <thead><tr className="border-b border-line text-left text-xs2 uppercase tracking-wider text-muted-fg">
          <th className="w-8 px-3 py-2" /><th className="px-2 py-2 font-medium">Issue</th><th className="px-2 py-2 font-medium">Status</th><th className="px-2 py-2 font-medium">Waits for</th><th className="px-2 py-2 text-right font-medium">Cost</th><th className="px-2 py-2 font-medium">Change request</th><th className="px-3 py-2 font-medium">Latest run</th></tr></thead>
        <tbody>{rows.map(({ i, d, kids }) => (
          <tr key={i.key} className={cx('group border-b border-line last:border-0 hover:bg-hover', fin(i.status) && 'opacity-60')}>
            <td className="px-3 py-2"><input id={'sel-' + i.key} type="checkbox" aria-label={'select ' + i.key} className="accent-[var(--primary)]" disabled={fin(i.status) || kids > 0 && i.status === 'running'} checked={sel.has(i.key)} onChange={() => toggle(i.key)} /></td>
            <td className="px-2 py-2"><div className="flex items-center gap-1.5" style={{ paddingLeft: d * 20 }}>
              {kids ? <button aria-label="toggle children" onClick={() => setCollapsed({ ...collapsed, [i.key]: !collapsed[i.key] })} className="rounded p-0.5 text-muted-fg hover:bg-muted"><Icon name={collapsed[i.key] ? 'right' : 'down'} className="h-3.5 w-3.5" /></button> : <span className="w-[18px]" />}
              <TypeIcon type={i.type} />
              <a href={'#issue-' + i.key} onClick={(e) => { e.preventDefault(); nav({ page: 'issue', key: i.key }); }} className="flex min-w-0 items-baseline gap-2 hover:underline">
                <span className="mono shrink-0 text-xs text-muted-fg">{i.key}</span><span className={cx('truncate', kids && 'font-medium')}>{i.title}</span></a>
              {i.start_requested_at && <span className="rounded bg-muted px-1.5 text-xs2 text-muted-fg" title="someone asked to start it">requested</span>}
            </div></td>
            <td className="px-2 py-2"><div className="flex items-center gap-3"><Status s={i.status} />{kids > 0 && <Progress k={i.key} />}</div></td>
            <td className="px-2 py-2">{i.depends_on.map((x, n) => <Fragment key={x}>{n > 0 && ', '}<IssueLink k={x} nav={nav} /></Fragment>)}</td>
            <td className="tnum px-2 py-2 text-right">{kids ? <span title={'own ' + usd(i.cost_usd) + ' + ' + usd(totalCost(i.key) - i.cost_usd) + ' from ' + leaves(i.key).length + ' issues'}><span className="font-medium">{usd(totalCost(i.key))}</span><span className="block text-xs2 text-muted-fg">own {usd(i.cost_usd)}</span></span> : i.cost_usd > 0 ? usd(i.cost_usd) : <span className="text-muted-fg">-</span>}</td>
            <td className="px-2 py-2">{i.change_request && <span className="inline-flex items-center gap-1 text-xs"><Icon name="pr" className={cx('h-3.5 w-3.5', i.change_request.status === 'merged' ? 'text-ok' : 'text-primary')} />#{i.change_request.number}</span>}</td>
            <td className="px-3 py-2">{i.latest_run && <a href={'#run-' + i.latest_run.id} onClick={(e) => { e.preventDefault(); nav({ page: 'run', id: i.latest_run.id }); }} className="hover:underline"><Status s={i.latest_run.status} /></a>}</td>
          </tr>))}</tbody>
      </table></div>
    </Card> : <DepGraph project={project} nav={nav} />}
  </div>;
}

function DepGraph({ project, nav }) {
  const pos = { 'gc-82': [0, 0], 'gc-83': [1, 0], 'gc-84': [2, 0], 'gc-86': [1, 1], 'gc-87': [0, 2], 'gc-89': [0, 3], 'gc-78': [0, 4] };
  const nodes = Object.keys(pos).filter(k => byKey[k].project === project);
  const W = 200, H = 44, GX = 80, GY = 22, P = 16;
  const xy = (k) => [P + pos[k][0] * (W + GX), P + pos[k][1] * (H + GY)];
  const color = { ok: 'var(--ok)', bad: 'var(--bad)', primary: 'var(--primary)', warn: 'var(--warn)' };
  if (!nodes.length) return <Card className="p-8 text-center text-muted-fg">No dependencies between open issues.</Card>;
  return <Card className="overflow-x-auto scroll-thin p-2">
    <svg width={P * 2 + 3 * W + 2 * GX} height={P * 2 + 5 * (H + GY)} className="block">
      {nodes.flatMap(k => byKey[k].depends_on.filter(d => pos[d]).map(d => { const [x1, y1] = xy(d), [x2, y2] = xy(k);
        return <path key={k + d} d={'M' + (x1 + W) + ' ' + (y1 + H / 2) + ' C' + (x1 + W + GX / 2) + ' ' + (y1 + H / 2) + ' ' + (x2 - GX / 2) + ' ' + (y2 + H / 2) + ' ' + (x2 - 6) + ' ' + (y2 + H / 2)} fill="none" stroke="var(--muted-fg)" strokeWidth="1.3" markerEnd="url(#arrow)" />; }))}
      <defs><marker id="arrow" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto"><path d="M0 0L10 5L0 10z" fill="var(--muted-fg)" /></marker></defs>
      {nodes.map(k => { const [x, y] = xy(k), i = byKey[k], t = TONE[i.status];
        return <g key={k} className="cursor-pointer" onClick={() => nav({ page: 'issue', key: k })}>
          <rect x={x} y={y} width={W} height={H} rx="8" fill="var(--card)" stroke={color[t] || 'var(--border)'} strokeWidth="1.5" />
          <text x={x + 12} y={y + 18} fill="var(--muted-fg)" fontFamily="var(--font-mono)" fontSize="11">{k} · {LABEL[i.status] || i.status}</text>
          <text x={x + 12} y={y + 34} fill="var(--fg)" fontSize="12.5">{i.title.length > 28 ? i.title.slice(0, 27) + '…' : i.title}</text></g>; })}
    </svg>
    <p className="px-2 pb-1 text-xs text-muted-fg">An arrow points from an issue to the one that waits for it. Click an issue to open it.</p>
  </Card>;
}

// ---------- Issue ----------
function IssuePage({ k, tab, nav, toast }) {
  const i = byKey[k], d = detailFor(k), kids = childrenOf(k), total = totalCost(k);
  const t = tab || 'overview';
  const approval = APPROVALS.find(a => a.issue === k);
  const actions = i.status === 'ready' || i.status === 'draft' ? [['Start', 'play', 'default']] : i.status === 'failed' ? [['Retry', 'retry', 'default']]
    : kids.length && i.status !== 'merged' ? [['Complete', 'check', 'outline'], ['Cancel', 'x', 'destructive']] : i.status === 'merged' || i.status === 'canceled' ? [] : [['Cancel', 'x', 'destructive']];
  const setTab = (x) => nav({ page: 'issue', key: k, tab: x });
  return <div className="space-y-5">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0"><div className="flex items-center gap-2 text-xs text-muted-fg"><TypeIcon type={i.type} /><span className="mono">{k}</span><span>·</span><span>{i.type}</span><span>·</span>
        <a href="#" onClick={e => e.preventDefault()} className="inline-flex items-center gap-1 hover:text-fg">{d.source_ref}<Icon name="ext" className="h-3 w-3" /></a></div>
        <h1 className="mt-1 flex flex-wrap items-center gap-3 text-xl font-semibold tracking-tight">{i.title}<Status s={i.status} /></h1></div>
      <div className="flex gap-2">{actions.map(([l, ic, v]) => <Button key={l} variant={v} onClick={() => toast(l + ' ' + k + ': sent.')}><Icon name={ic} className="h-3.5 w-3.5" />{l}</Button>)}</div>
    </div>
    {approval && <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-warn-soft px-4 py-3">
      <div className="flex min-w-0 items-start gap-3"><span className="mt-0.5 text-warn"><Icon name={approval.kind === 'question' ? 'question' : approval.kind === 'protected' ? 'shield' : 'gate'} /></span>
        <div className="min-w-0"><div className="text-[13px] font-semibold">{approval.title}</div><p className="text-[13px] text-muted-fg">{approval.reason}</p></div></div>
      <div className="flex gap-2"><Button variant="outline" size="sm" onClick={() => nav({ page: 'approvals' })}>Open in Approvals</Button>{approval.actions && <Button size="sm" onClick={() => toast('Approved. The run continues.')}><Icon name="check" className="h-3.5 w-3.5" />Approve</Button>}</div>
    </div>}
    <Card className="grid grid-cols-2 sm:grid-cols-4 sm:divide-x divide-line">
      <Stat label={kids.length ? 'Total cost' : 'Cost'} value={usd(total)} sub={kids.length ? 'own ' + usd(i.cost_usd) + ' + ' + leaves(k).length + ' issues under it' : (d.usage.input_tokens + d.usage.output_tokens).toLocaleString('en-US') + ' tokens'} />
      {kids.length ? <Stat label="Progress" value={leaves(k).filter(x => x.status === 'merged').length + ' / ' + leaves(k).length} sub="issues merged" />
        : <Stat label="Change request" value={i.change_request ? '#' + i.change_request.number : '-'} sub={i.change_request ? i.change_request.status + ' · agent/' + k + '/…' : 'none yet'} />}
      <Stat label="Runs" value={d.runs.length} sub={d.runs[0] ? 'latest: ' + d.runs[0].status : 'not started'} />
      <Stat label="Waits for" value={i.depends_on.length || 'nothing'} sub={i.depends_on.join(', ') || 'can start now'} />
    </Card>
    <Tabs value={t} onChange={setTab} tabs={[{ id: 'overview', label: 'Overview' }, { id: 'runs', label: 'Runs', count: d.runs.length }, ...(kids.length ? [{ id: 'children', label: 'Issues', count: leaves(k).length }] : []), { id: 'activity', label: 'Activity' }]} />
    {t === 'overview' && <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_280px]">
      <div className="min-w-0 space-y-5">
        {i.latest_run && i.latest_run.status === 'running' && <LiveRunCard id={i.latest_run.id} nav={nav} />}
        <Card><CardHead title="Spec" sub={d.spec_approved_by ? 'approved by ' + d.spec_approved_by : 'from the GitHub issue'} />
          <div className="max-w-[68ch] space-y-3 whitespace-pre-wrap px-4 py-3 text-[13.5px] leading-relaxed">{d.spec}</div></Card>
        {d.acceptance_criteria.length > 0 && <Card><CardHead title="Acceptance criteria" sub="the tests are derived from these" />
          <ul className="divide-y divide-line">{d.acceptance_criteria.map((c, n) => <li key={n} className="flex gap-3 px-4 py-2 text-[13px]"><span className="tnum mono w-4 shrink-0 text-muted-fg">{n + 1}</span><span className="min-w-0 break-words">{c}</span></li>)}</ul></Card>}
        {kids.length > 0 && <ChildrenTable k={k} nav={nav} compact />}
      </div>
      <aside className="space-y-5">
        <Card><CardHead title="Relations" />
          <dl className="space-y-2.5 px-4 py-3 text-[13px]">
            {i.parent && <div><dt className="text-xs text-muted-fg">Parent</dt><dd><IssueLink k={i.parent} nav={nav} showTitle /></dd></div>}
            <div><dt className="text-xs text-muted-fg">Waits for</dt><dd>{i.depends_on.length ? i.depends_on.map(x => <div key={x}><IssueLink k={x} nav={nav} showTitle /> <Status s={byKey[x].status} /></div>) : <span className="text-muted-fg">nothing</span>}</dd></div>
            <div><dt className="text-xs text-muted-fg">Blocks</dt><dd>{dependents(k).length ? dependents(k).map(x => <div key={x.key}><IssueLink k={x.key} nav={nav} showTitle /></div>) : <span className="text-muted-fg">nothing</span>}</dd></div>
          </dl></Card>
        {d.overlaps.length > 0 && <div className="rounded-lg border border-line bg-warn-soft px-4 py-3 text-[13px]"><div className="flex items-center gap-2 font-medium text-warn"><Icon name="alert" className="h-3.5 w-3.5" />Overlaps with in-flight work</div>
          {d.overlaps.map(o => <p key={o.with} className="mt-1 text-muted-fg"><IssueLink k={o.with} nav={nav} /> also touches <span className="mono text-xs">{o.paths.join(', ')}</span></p>)}</div>}
        {d.changed_paths.length > 0 && <Card><CardHead title="Changed paths" sub={'on ' + (i.change_request ? '#' + i.change_request.number : 'the branch')} />
          <ul className="space-y-1 px-4 py-3">{d.changed_paths.map(p => <li key={p} className="mono truncate text-xs" title={p}>{p}</li>)}</ul></Card>}
        <Card><CardHead title="Details" /><dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 px-4 py-3 text-[13px]">
          <dt className="text-muted-fg">Project</dt><dd>{PROJECTS.find(p => p.id === i.project).name}</dd>
          <dt className="text-muted-fg">Priority</dt><dd>P{d.priority}</dd>
          <dt className="text-muted-fg">Labels</dt><dd className="flex flex-wrap gap-1">{i.labels.map(l => <span key={l} className="rounded bg-muted px-1.5 text-xs2">{l}</span>)}</dd>
          <dt className="text-muted-fg">Synced by</dt><dd>{d.created_by}</dd></dl></Card>
      </aside>
    </div>}
    {t === 'runs' && <div className="space-y-5">{i.latest_run && i.latest_run.status === 'running' && <LiveRunCard id={i.latest_run.id} nav={nav} />}<RunsTable rows={d.runs.map(r => ({ ...r, issue: k, step: r.status === 'running' ? RUNS[r.id]?.current : null, started: r.created_at, duration: r.status === 'running' ? '34m' : '21m', cost: r.cost_usd }))} nav={nav} hideIssue /></div>}
    {t === 'children' && <ChildrenTable k={k} nav={nav} />}
    {t === 'activity' && <Card><ol className="relative space-y-0 px-4 py-3">{d.activity.map((a, n) => <li key={n} className="flex gap-4 border-l border-line pb-4 pl-4 last:pb-0">
      <span className="-ml-[21px] mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full border-2 border-card bg-muted-fg" /><span className="tnum mono w-11 shrink-0 text-xs text-muted-fg">{a.t}</span><span className="min-w-0 text-[13px]"><span className="font-medium">{a.who}</span> {a.what}</span></li>)}</ol></Card>}
  </div>;
}

function ChildrenTable({ k, nav, compact }) {
  const rows = []; const walk = (x, dd) => childrenOf(x).forEach(c => { rows.push({ c, dd }); walk(c.key, dd + 1); }); walk(k, 0);
  return <Card className="overflow-hidden"><CardHead title={'Issues under ' + k} sub={rows.length + ' issues · total ' + usd(totalCost(k))} />
    <div className="overflow-x-auto scroll-thin"><table className="w-full min-w-[520px] text-[13px]"><tbody>{rows.map(({ c, dd }) => (
      <tr key={c.key} className="border-b border-line last:border-0 hover:bg-hover"><td className="px-4 py-2"><div className="flex items-center gap-2" style={{ paddingLeft: dd * 18 }}><TypeIcon type={c.type} /><IssueLink k={c.key} nav={nav} /><span className="truncate">{c.title}</span></div></td>
        <td className="px-2 py-2"><Status s={c.status} /></td><td className="tnum px-4 py-2 text-right">{usd(totalCost(c.key))}</td></tr>))}
      <tr className="bg-muted"><td className="px-4 py-2 font-medium" colSpan="2">Total, including {k} itself ({usd(byKey[k].cost_usd)})</td><td className="tnum px-4 py-2 text-right font-semibold">{usd(totalCost(k))}</td></tr>
    </tbody></table></div></Card>;
}

function StepPill({ s, onClick, active }) {
  const t = TONE[s.status];
  const ring = { ok: 'border-ok', bad: 'border-bad', primary: 'border-primary', warn: 'border-warn' }[t] || 'border-line';
  return <button onClick={onClick} className={cx('flex shrink-0 items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-xs', ring, s.status === 'skipped' && 'border-dashed opacity-60', s.status === 'pending' && 'opacity-60', active && 'ring-2 ring-[var(--ring)]')}>
    <Icon name={s.kind === 'agent' ? 'bot' : s.kind === 'gate' ? 'gate' : 'queue'} className="h-3.5 w-3.5 text-muted-fg" /><span className="mono">{s.step_id}{s.attempt > 1 ? ' #' + s.attempt : ''}</span>
    {s.status === 'running' && <span className="pulse h-1.5 w-1.5 rounded-full bg-primary" />}</button>;
}
function LiveRunCard({ id, nav }) {
  const r = runFor(id); const cur = r.steps.find(s => s.status === 'running');
  const last = cur ? transcriptFor(cur).slice(-1)[0] : null;
  return <Card className="overflow-hidden"><div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2.5">
      <div className="flex items-center gap-2 text-[13px]"><span className="pulse h-2 w-2 rounded-full bg-primary" /><span className="font-semibold">Run {r.n} is live</span><span className="text-muted-fg">· {r.workflow} · {r.duration} · {usd(r.cost_usd)}</span></div>
      <Button variant="outline" size="sm" onClick={() => nav({ page: 'run', id })}>Open run<Icon name="right" className="h-3.5 w-3.5" /></Button></div>
    <div className="flex items-center gap-1.5 overflow-x-auto scroll-thin px-4 py-3">{r.steps.map((s, n) => <Fragment key={s.id}>{n > 0 && <span className="h-px w-3 shrink-0 bg-line" />}<StepPill s={s} onClick={() => nav({ page: 'run', id, step: s.id })} /></Fragment>)}</div>
    {last && <div className="border-t border-line bg-code px-4 py-2.5 text-[13px]"><span className="text-xs text-muted-fg">{cur.step_id} · {cur.role} · {cur.model}</span><p className="mt-0.5 line-clamp-2">{last.body}</p></div>}
  </Card>;
}

// ---------- Run ----------
function RunPage({ id, step, nav, toast }) {
  const r = runFor(id), i = byKey[r.issue];
  const sid = step || (r.steps.find(s => s.status === 'running') || r.steps[0]).id;
  const s = r.steps.find(x => x.id === sid) || r.steps[0];
  const ev = transcriptFor(s);
  const [steer, setSteer] = useState('');
  return <div className="space-y-5">
    <PageHead title={'Run ' + r.n} icon={<Status s={r.status} className="order-last" />}
      sub={<span>{r.workflow} for <IssueLink k={r.issue} nav={nav} showTitle /> · started {r.started} · {r.duration} · {usd(r.cost_usd)}</span>}
      right={r.status === 'running' && <Button variant="destructive" onClick={() => toast('Abort sent to the running step.')}><Icon name="stop" className="h-3.5 w-3.5" />Abort run</Button>} />
    <div className="grid gap-5 lg:grid-cols-[300px_minmax(0,1fr)]">
      <Card className="h-fit overflow-hidden"><CardHead title="Steps" sub="workflow snapshotted at start" />
        <ol className="py-1">{r.steps.map(x => <li key={x.id}>
          <button onClick={() => nav({ page: 'run', id, step: x.id })} className={cx('flex w-full items-start gap-3 px-4 py-2 text-left hover:bg-hover', x.id === s.id && 'bg-primary-soft', x.parallel && 'pl-8')}>
            <span className={cx('mt-0.5', { ok: 'text-ok', bad: 'text-bad', primary: 'text-primary', warn: 'text-warn' }[TONE[x.status]] || 'text-muted-fg')}><Icon name={x.kind === 'agent' ? 'bot' : x.kind === 'gate' ? 'gate' : 'queue'} /></span>
            <span className="min-w-0 flex-1"><span className="flex items-center justify-between gap-2"><span className="mono text-[12.5px] font-medium">{x.step_id}{x.attempt > 1 && <span className="text-muted-fg"> #{x.attempt}</span>}</span><Status s={x.status} /></span>
              <span className="block truncate text-xs text-muted-fg">{x.kind === 'agent' ? x.role + ' · ' + x.model.split('/').pop() : x.gate ? x.gate + ' gate' + (x.role ? ' · ' + x.role : '') : 'merge queue'}{x.dur ? ' · ' + x.dur : ''}{x.cost_usd ? ' · ' + usd(x.cost_usd) : ''}</span>
              {x.loop && <span className="mt-1 flex items-center gap-1 text-xs text-bad"><Icon name="loop" className="h-3 w-3" />{x.loop}</span>}
              {x.skipped_because && <span className="block text-xs text-muted-fg">skipped: {x.skipped_because}</span>}</span>
          </button></li>)}</ol></Card>
      <Card className="flex min-w-0 flex-col overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2.5"><div className="min-w-0"><h3 className="mono text-[13px] font-semibold">{s.step_id}{s.attempt > 1 ? ' #' + s.attempt : ''}</h3>
          <p className="truncate text-xs text-muted-fg">{[s.role, s.harness, s.model, s.commit_sha && 'at ' + s.commit_sha].filter(Boolean).join(' · ') || s.kind}</p></div>
          <div className="flex items-center gap-2">{s.status === 'running' && <span className="flex items-center gap-1.5 text-xs text-primary"><span className="pulse h-2 w-2 rounded-full bg-primary" />Live</span>}<Status s={s.status} /></div></div>
        <div className="max-h-[560px] flex-1 divide-y divide-line overflow-y-auto scroll-thin">
          {ev.length === 0 && <p className="px-4 py-8 text-center text-[13px] text-muted-fg">{s.status === 'skipped' ? 'Skipped: ' + s.skipped_because + '.' : 'This step has not started. It runs after review.'}</p>}
          {ev.map((e, n) => <Event key={n} e={e} />)}
        </div>
        {s.status === 'running' && <div className="border-t border-line bg-muted px-4 py-3"><label htmlFor="steer" className="text-xs font-medium text-muted-fg">Steer the {s.role}: the message reaches the session without stopping it</label>
          <div className="mt-1.5 flex gap-2"><input id="steer" value={steer} onChange={e => setSteer(e.target.value)} placeholder="Check ordering against criterion 5 before you approve" className="h-8 min-w-0 flex-1 rounded-md border border-line bg-card px-2.5 text-[13px] placeholder:text-muted-fg" />
            <Button disabled={!steer} onClick={() => { toast('Steer sent to ' + s.step_id + '.'); setSteer(''); }}><Icon name="send" className="h-3.5 w-3.5" />Steer</Button>
            <Button variant="destructive" onClick={() => toast('Abort sent to ' + s.step_id + '.')}>Abort step</Button></div></div>}
      </Card>
    </div>
  </div>;
}
function Event({ e }) {
  if (e.type === 'gate') return <div className="px-4 py-3"><div className="flex items-center justify-between gap-2"><span className="mono flex items-center gap-2 text-[12.5px] font-medium"><Icon name="terminal" className="h-3.5 w-3.5 text-muted-fg" />{e.title}</span><Status s={e.status} /></div>
    <pre className={cx('mono mt-2 overflow-x-auto rounded-md bg-code p-3 text-xs leading-relaxed scroll-thin', e.status === 'failed' && 'text-bad')}>{e.body}</pre></div>;
  if (e.type === 'tool') return <details className="group px-4 py-2" open={e.name === 'Bash'}><summary className="flex cursor-pointer list-none items-center gap-2 text-[12.5px]"><Icon name="right" className="h-3 w-3 text-muted-fg transition-transform group-open:rotate-90" /><span className="rounded bg-muted px-1.5 font-medium">{e.name}</span><span className="mono truncate text-xs text-muted-fg">{e.body.split('\n')[0]}</span></summary>
    {e.body.includes('\n') && <pre className="mono mt-2 overflow-x-auto rounded-md bg-code p-3 text-xs leading-relaxed scroll-thin">{e.body}</pre>}</details>;
  const who = { user: ['Frankie', 'user'], assistant: ['Agent', 'bot'], result: ['Result', 'check'] }[e.type];
  return <div className={cx('flex gap-3 px-4 py-3', e.type === 'result' && 'bg-ok-soft')}><span className="mt-0.5 text-muted-fg"><Icon name={who[1]} /></span>
    <div className="min-w-0"><div className="text-xs font-medium text-muted-fg">{who[0]}{e.live && <span className="pulse ml-2 text-primary">writing…</span>}</div><p className="mt-0.5 max-w-[72ch] whitespace-pre-wrap text-[13px] leading-relaxed">{e.body}</p></div></div>;
}

// ---------- Runs ----------
function RunsTable({ rows, nav, hideIssue }) {
  return <Card className="overflow-hidden"><div className="overflow-x-auto scroll-thin"><table className="w-full min-w-[680px] text-[13px]">
    <thead><tr className="border-b border-line text-left text-xs2 uppercase tracking-wider text-muted-fg"><th className="px-4 py-2 font-medium">Run</th>{!hideIssue && <th className="px-2 py-2 font-medium">For</th>}<th className="px-2 py-2 font-medium">Workflow</th><th className="px-2 py-2 font-medium">Status</th><th className="px-2 py-2 font-medium">Now</th><th className="px-2 py-2 font-medium">Started</th><th className="px-2 py-2 font-medium">Took</th><th className="px-4 py-2 text-right font-medium">Cost</th></tr></thead>
    <tbody>{rows.map(r => <tr key={r.id} onClick={() => nav({ page: 'run', id: r.id })} className="cursor-pointer border-b border-line last:border-0 hover:bg-hover">
      <td className="px-4 py-2 font-medium">Run {r.n}</td>
      {!hideIssue && <td className="px-2 py-2">{r.issue ? <span onClick={e => e.stopPropagation()}><IssueLink k={r.issue} nav={nav} showTitle /></span> : <span className="mono text-xs">release {r.release}</span>}</td>}
      <td className="mono px-2 py-2 text-xs">{r.workflow}</td><td className="px-2 py-2"><Status s={r.status} /></td><td className="mono px-2 py-2 text-xs text-muted-fg">{r.step || '-'}</td>
      <td className="tnum px-2 py-2 text-muted-fg">{r.started}</td><td className="tnum px-2 py-2 text-muted-fg">{r.duration}</td><td className="tnum px-4 py-2 text-right">{usd(r.cost)}</td></tr>)}</tbody></table></div></Card>;
}
function RunsPage({ project, nav }) {
  const [f, setF] = useState('all');
  const mine = ALL_RUNS.filter(r => r.issue ? byKey[r.issue].project === project : project === 'gc');
  const rows = mine.filter(r => f === 'all' || (f === 'active' ? ['running', 'awaiting_human'].includes(r.status) : r.status === f));
  return <div className="space-y-5"><PageHead title="Runs" sub="every workflow run of the project, newest first" right={<Segmented value={f} onChange={setF} options={[{ id: 'all', label: 'All' }, { id: 'active', label: 'Active' }, { id: 'failed', label: 'Failed' }]} />} />
    <RunsTable rows={rows} nav={nav} /></div>;
}

// ---------- Approvals ----------
function ApprovalsPage({ nav, toast, done, setDone }) {
  const [f, setF] = useState('all');
  const [answer, setAnswer] = useState('');
  const open = APPROVALS.filter(a => !done.has(a.id));
  const rows = open.filter(a => f === 'all' || a.kind === f || (f === 'gate' && a.kind === 'protected'));
  const icon = { gate: 'gate', protected: 'shield', question: 'question', spec: 'doc', release: 'rocket' };
  const kindLabel = { gate: 'Human gate', protected: 'Protected paths', question: 'Question from an agent', spec: 'Spec approval', release: 'Release gate' };
  const decide = (a, what) => { setDone(new Set([...done, a.id])); toast(what + ': ' + a.title + '.'); };
  const count = (k) => open.filter(a => a.kind === k || (k === 'gate' && a.kind === 'protected')).length;
  return <div className="space-y-5">
    <PageHead title="Approvals" sub="everything that waits for a person, across all projects" right={<Segmented value={f} onChange={setF} options={[{ id: 'all', label: 'All ' + open.length }, { id: 'gate', label: 'Gates ' + count('gate') }, { id: 'question', label: 'Questions ' + count('question') }, { id: 'spec', label: 'Specs ' + count('spec') }, { id: 'release', label: 'Releases ' + count('release') }]} />} />
    {rows.length === 0 && <Card className="px-6 py-12 text-center"><div className="mx-auto mb-2 w-fit text-ok"><Icon name="check" className="h-6 w-6" /></div><p className="font-medium">Nothing waits for you</p><p className="text-[13px] text-muted-fg">New gates, questions and specs show up here and as a count in the sidebar.</p></Card>}
    {rows.map(a => <Card key={a.id} className="overflow-hidden">
      <div className="flex items-start gap-3 px-4 py-3">
        <span className={cx('mt-0.5 rounded-md p-1.5', a.kind === 'protected' ? 'bg-bad-soft text-bad' : 'bg-warn-soft text-warn')}><Icon name={icon[a.kind]} /></span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-fg"><span className="font-medium uppercase tracking-wider">{kindLabel[a.kind]}</span><span>·</span><span>{a.created}</span>{a.expires && <><span>·</span><span>expires {a.expires}</span></>}</div>
          <h3 className="mt-0.5 text-[14px] font-semibold">{a.title}</h3>
          <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
            {a.issue && <IssueLink k={a.issue} nav={nav} showTitle />}
            {a.run && <a href={'#run-' + a.run} onClick={e => { e.preventDefault(); nav({ page: 'run', id: a.run }); }} className="text-xs text-primary hover:underline">Run 1 · {a.step}</a>}
            {a.release && <a href="#release-rel-012" onClick={e => { e.preventDefault(); nav({ page: 'release', id: a.release }); }} className="text-xs text-primary hover:underline">Release v0.1.2</a>}
            {a.cr && <span className="inline-flex items-center gap-1 text-xs text-muted-fg"><Icon name="pr" className="h-3.5 w-3.5" />#{a.cr} at <span className="mono">{a.commit}</span></span>}
          </div>
          <p className="mt-2 max-w-[75ch] text-[13px] leading-relaxed text-muted-fg">{a.reason}</p>
          {a.paths && <ul className="mt-2 rounded-md bg-code px-3 py-2">{a.paths.map(p => <li key={p} className="mono text-xs">{p}</li>)}</ul>}
        </div></div>
      <div className="border-t border-line bg-muted px-4 py-3">
        {a.kind === 'question' ? <div className="flex flex-col gap-2 sm:flex-row"><textarea id={'answer-' + a.id} value={answer} onChange={e => setAnswer(e.target.value)} rows="2" placeholder="Your answer goes to the agent and to later steps of the issue" className="min-w-0 flex-1 rounded-md border border-line bg-card px-2.5 py-1.5 text-[13px] placeholder:text-muted-fg" />
          <Button className="self-end" disabled={!answer} onClick={() => decide(a, 'Answered')}><Icon name="send" className="h-3.5 w-3.5" />Answer</Button></div>
        : a.kind === 'spec' ? <div className="flex flex-wrap gap-2"><Button onClick={() => decide(a, 'Spec approved')}><Icon name="check" className="h-3.5 w-3.5" />Approve spec</Button><Button variant="outline" onClick={() => nav({ page: 'issue', key: a.issue })}>Read the refined spec</Button><Button variant="ghost" onClick={() => decide(a, 'Sent back to the refiner')}>Send back with feedback</Button></div>
        : <div className="flex flex-wrap items-center gap-2"><Button onClick={() => decide(a, 'Approved')}><Icon name="check" className="h-3.5 w-3.5" />Approve</Button>
          {a.actions.includes('request-changes') && <Button variant="outline" onClick={() => decide(a, 'Changes requested')}>Request changes</Button>}
          <Button variant="destructive" onClick={() => decide(a, 'Rejected')}><Icon name="x" className="h-3.5 w-3.5" />Reject</Button>
          <span className="text-xs text-muted-fg">Your decision is recorded with your name once sign-in exists.</span></div>}
      </div></Card>)}
  </div>;
}

// ---------- Releases ----------
function Pipeline({ stages, big }) {
  return <div className="flex flex-wrap items-center gap-1.5">{stages.map((s, n) => <Fragment key={s.stage}>{n > 0 && <Icon name="right" className="h-3.5 w-3.5 text-muted-fg" />}
    <span className={cx('inline-flex items-center gap-1.5', big && 'rounded-md border border-line bg-card px-2.5 py-1.5')}><span className="mono text-xs">{s.stage}</span><Status s={s.status} /></span></Fragment>)}</div>;
}
function ReleasesPage({ project, nav }) {
  if (project !== 'gc') return <div className="space-y-5"><PageHead title="Releases" sub="no stages configured for this project" /><Card className="px-6 py-12 text-center text-[13px] text-muted-fg">This project has no stages in .factory/config.yaml, so merges are not released.</Card></div>;
  return <div className="space-y-5"><PageHead title="Releases" sub="every merge from the queue asks for a release; stages from .factory/config.yaml" />
    <Card className="overflow-hidden"><div className="overflow-x-auto scroll-thin"><table className="w-full min-w-[700px] text-[13px]">
      <thead><tr className="border-b border-line text-left text-xs2 uppercase tracking-wider text-muted-fg"><th className="px-4 py-2 font-medium">Release</th><th className="px-2 py-2 font-medium">Status</th><th className="px-2 py-2 font-medium">Stages</th><th className="px-2 py-2 font-medium">Issues</th><th className="px-2 py-2 font-medium">Created</th><th className="px-4 py-2 text-right font-medium">Agent cost</th></tr></thead>
      <tbody>{RELEASES.map(r => <tr key={r.id} onClick={() => nav({ page: 'release', id: r.id })} className="cursor-pointer border-b border-line last:border-0 hover:bg-hover">
        <td className="px-4 py-2"><span className="font-semibold">{r.version || 'unversioned'}</span> <span className="mono text-xs text-muted-fg">{r.sha}</span></td>
        <td className="px-2 py-2"><Status s={r.status} /></td><td className="px-2 py-2"><Pipeline stages={r.stages} /></td>
        <td className="px-2 py-2">{r.issues.map((k, n) => <span key={k} onClick={e => e.stopPropagation()}>{n > 0 && ', '}<IssueLink k={k} nav={nav} /></span>)}</td>
        <td className="tnum px-2 py-2 text-muted-fg">{r.created}</td><td className="tnum px-4 py-2 text-right">{usd(r.cost_usd)}</td></tr>)}</tbody></table></div></Card></div>;
}
function ReleasePage({ id, nav, toast, done, setDone }) {
  const r = RELEASES.find(x => x.id === id) || RELEASES[0];
  const gate = r.id === 'rel-012' && !done.has('ap5');
  return <div className="space-y-5">
    <PageHead title={r.version || 'Release ' + r.sha} icon={<Icon name="rocket" className="h-5 w-5 text-muted-fg" />} sub={<span>Goodreads-Clone at <span className="mono">{r.sha}</span> · {r.bump ? r.bump + ' bump proposed by the release agent · ' : ''}{r.created}</span>} right={<Status s={r.status} />} />
    {gate && <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-line bg-warn-soft px-4 py-3"><div className="text-[13px]"><div className="font-semibold">Production waits for you</div><p className="text-muted-fg">Staging passed 381 of 381 tests. Approving deploys v0.1.2 to production; rejecting leaves production on v0.1.1.</p></div>
      <div className="flex gap-2"><Button onClick={() => { setDone(new Set([...done, 'ap5'])); toast('Approved: v0.1.2 deploys to production.'); }}><Icon name="check" className="h-3.5 w-3.5" />Approve</Button><Button variant="destructive" onClick={() => { setDone(new Set([...done, 'ap5'])); toast('Rejected: production stays on v0.1.1.'); }}>Reject</Button></div></div>}
    <Card className="px-4 py-3"><Pipeline stages={r.stages} big /></Card>
    <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_300px]">
      <div className="min-w-0 space-y-5">
        <Card><CardHead title="Staging verification" sub="the project's e2e suite against the deployed stage" right={<Status s={r.verification.staging.passed ? 'succeeded' : 'failed'} />} />
          <div className="px-4 py-3 text-[13px]"><p className="tnum font-medium">{r.verification.staging.summary}</p>
            {r.verification.staging.failures && <ul className="mt-2 divide-y divide-line rounded-md border border-line">{r.verification.staging.failures.map(f => <li key={f} className="mono px-3 py-1.5 text-xs text-bad">{f}</li>)}</ul>}
            {r.bug && <p className="mt-2 text-muted-fg">Rolled back to the previous tag. Bug issue {r.bug} carries the release agent's analysis.</p>}</div></Card>
        {r.notes && <Card><CardHead title="Release notes" sub="written by the release agent" /><div className="whitespace-pre-wrap px-4 py-3 text-[13px] leading-relaxed">{r.notes}</div></Card>}
      </div>
      <aside className="space-y-5"><Card><CardHead title="Issues in this release" /><ul className="space-y-1.5 px-4 py-3">{r.issues.length ? r.issues.map(k => <li key={k}><IssueLink k={k} nav={nav} showTitle /></li>) : <li className="text-[13px] text-muted-fg">None merged by Frankie.</li>}</ul></Card>
        <Card><CardHead title="Release agent" /><dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 px-4 py-3 text-[13px]"><dt className="text-muted-fg">Runs</dt><dd>notes{r.bug ? ', analysis' : ''}</dd><dt className="text-muted-fg">Cost</dt><dd className="tnum">{usd(r.cost_usd)}</dd></dl></Card></aside>
    </div></div>;
}

// ---------- Shell ----------
function crumbsFor(route, project) {
  const p = PROJECTS.find(x => x.id === project).name;
  const top = { page: 'board' };
  if (route.page === 'issue') return [[p, top], ...ancestry(route.key).map(a => [a.key + ' ' + a.title, { page: 'issue', key: a.key }]), [route.key + ' ' + byKey[route.key].title]];
  if (route.page === 'run') { const r = runFor(route.id); return [[p, top], ...ancestry(r.issue).map(a => [a.key, { page: 'issue', key: a.key }]), [r.issue, { page: 'issue', key: r.issue }], ['Run ' + r.n, route.step ? { page: 'run', id: route.id } : null], ...(route.step ? [[r.steps.find(s => s.id === route.step)?.step_id]] : [])]; }
  if (route.page === 'release') return [[p, top], ['Releases', { page: 'releases' }], [(RELEASES.find(x => x.id === route.id) || {}).version || 'Release']];
  return [[p, top], [{ board: 'Board', runs: 'Runs', approvals: 'Approvals', releases: 'Releases' }[route.page]]];
}
function Sidebar({ route, nav, project, setProject, waiting, onClose }) {
  const [open, setOpen] = useState(false);
  const p = PROJECTS.find(x => x.id === project);
  const items = [['board', 'Board', 'board'], ['runs', 'Runs', 'runs'], ['approvals', 'Approvals', 'inbox'], ['releases', 'Releases', 'rocket']];
  const active = route.page === 'issue' ? 'board' : route.page === 'run' ? 'runs' : route.page === 'release' ? 'releases' : route.page;
  return <nav className="flex h-full flex-col gap-4 bg-sidebar px-3 py-4" style={{ paddingLeft: 'max(12px, env(safe-area-inset-left, 0px))' }}>
    <div className="flex items-center gap-2 px-2"><span className="grid h-7 w-7 place-items-center rounded-md bg-primary text-[13px] font-bold text-primary-fg">F</span><span className="text-[15px] font-semibold tracking-tight">Frankie</span>
      {onClose && <Button variant="ghost" size="icon" className="ml-auto" onClick={onClose} aria-label="close menu"><Icon name="x" /></Button>}</div>
    <div className="relative">
      <button onClick={() => setOpen(!open)} className="flex w-full items-center gap-2 rounded-md border border-line bg-card px-2.5 py-2 text-left hover:bg-hover">
        <span className="mono grid h-6 w-7 place-items-center rounded bg-muted text-xs2 font-medium">{p.key_prefix}</span><span className="min-w-0 flex-1"><span className="block truncate text-[13px] font-medium">{p.name}</span><span className="block truncate text-xs2 text-muted-fg">{p.repo}</span></span><Icon name="updown" className="h-3.5 w-3.5 text-muted-fg" /></button>
      {open && <div className="absolute inset-x-0 top-full z-20 mt-1 rounded-md border border-line bg-card p-1 shadow-lg">{PROJECTS.map(x => <button key={x.id} onClick={() => { setOpen(false); nav({ page: 'board', project: x.id }); }} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-[13px] hover:bg-hover"><span className="mono w-8 text-xs text-muted-fg">{x.key_prefix}</span>{x.name}{x.id === project && <Icon name="check" className="ml-auto h-3.5 w-3.5 text-primary" />}</button>)}
        <div className="mt-1 border-t border-line px-2 py-1.5 text-xs text-muted-fg">Connect a repository: Settings, after auth</div></div>}
    </div>
    <ul className="space-y-0.5">{items.map(([id, label, ic]) => <li key={id}><button onClick={() => nav({ page: id })} className={cx('flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] font-medium', active === id ? 'bg-primary-soft text-primary' : 'text-muted-fg hover:bg-hover hover:text-fg')}>
      <Icon name={ic} />{label}{id === 'approvals' && waiting > 0 && <span className="tnum ml-auto rounded-full bg-warn px-1.5 text-xs2 font-semibold text-bg">{waiting}</span>}</button></li>)}</ul>
    <div className="mt-auto space-y-0.5"><button disabled className="flex w-full cursor-not-allowed items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] font-medium text-muted-fg opacity-60"><Icon name="settings" />Settings<span className="ml-auto text-xs2">later</span></button>
      <div className="flex items-center gap-2.5 rounded-md px-2.5 py-2"><span className="grid h-6 w-6 place-items-center rounded-full bg-muted text-muted-fg"><Icon name="user" className="h-3.5 w-3.5" /></span><span className="min-w-0 text-xs"><span className="block font-medium">You</span><span className="block text-muted-fg">API token · sign-in comes next</span></span></div>
      <p className="px-2.5 pt-1 text-xs2 text-muted-fg">Mockup with example data</p></div>
  </nav>;
}
function Palette({ onClose, nav, project }) {
  const [q, setQ] = useState(''); const [n, setN] = useState(0); const ref = useRef(null);
  useEffect(() => { ref.current && ref.current.focus(); }, []);
  const pages = [['Board', { page: 'board' }, 'board'], ['Runs', { page: 'runs' }, 'runs'], ['Approvals', { page: 'approvals' }, 'inbox'], ['Releases', { page: 'releases' }, 'rocket']].map(([l, r, ic]) => ({ group: 'Pages', label: l, route: r, icon: ic }));
  const issues = ISSUES.map(i => ({ group: 'Issues', label: i.key + '  ' + i.title, route: { page: 'issue', key: i.key }, status: i.status, type: i.type }));
  const runs = ALL_RUNS.filter(r => r.issue).map(r => ({ group: 'Runs', label: 'Run ' + r.n + ' of ' + r.issue + ' · ' + r.workflow, route: { page: 'run', id: r.id }, status: r.status }));
  const all = [...pages, ...issues, ...runs].filter(x => x.label.toLowerCase().includes(q.toLowerCase())).slice(0, 12);
  const go = (x) => { nav(x.route); onClose(); };
  return <div className="fixed inset-0 z-40 grid place-items-start justify-center bg-black/40 px-4 pt-[12vh]" onClick={onClose}>
    <div className="w-full max-w-lg overflow-hidden rounded-xl border border-line bg-card shadow-2xl" onClick={e => e.stopPropagation()}>
      <div className="flex items-center gap-2 border-b border-line px-3"><Icon name="search" className="text-muted-fg" /><input id="palette" ref={ref} value={q} onChange={e => { setQ(e.target.value); setN(0); }}
        onKeyDown={e => { if (e.key === 'ArrowDown') { e.preventDefault(); setN(Math.min(n + 1, all.length - 1)); } if (e.key === 'ArrowUp') { e.preventDefault(); setN(Math.max(n - 1, 0)); } if (e.key === 'Enter' && all[n]) go(all[n]); if (e.key === 'Escape') onClose(); }}
        placeholder="Jump to an issue key, a run or a page" className="palette-input h-11 min-w-0 flex-1 bg-transparent text-[14px] outline-none placeholder:text-muted-fg" /><Kbd>esc</Kbd></div>
      <ul className="max-h-80 overflow-y-auto p-1 scroll-thin">{all.length === 0 && <li className="px-3 py-6 text-center text-[13px] text-muted-fg">No issue, run or page matches "{q}".</li>}
        {all.map((x, j) => <Fragment key={x.label}>{(j === 0 || all[j - 1].group !== x.group) && <li className="px-2 pb-1 pt-2 text-xs2 font-medium uppercase tracking-wider text-muted-fg">{x.group}</li>}
          <li><button onMouseEnter={() => setN(j)} onClick={() => go(x)} className={cx('flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px]', j === n && 'bg-hover')}>
            {x.type ? <TypeIcon type={x.type} /> : <Icon name={x.icon || 'runs'} className="h-3.5 w-3.5 text-muted-fg" />}<span className="min-w-0 flex-1 truncate">{x.label}</span>{x.status && <Status s={x.status} />}</button></li></Fragment>)}</ul>
    </div></div>;
}

// ---------- App ----------
function toHash(r) { return ['board', 'runs', 'releases'].includes(r.page) ? r.page + '-' + r.project : r.page === 'issue' ? 'issue-' + r.key + (r.tab && r.tab !== 'overview' ? '-' + r.tab : '') : r.page === 'run' ? 'run-' + r.id + (r.step ? '-' + r.step : '') : r.page === 'release' ? 'release-' + r.id : r.page; }
function fromHash(h) {
  h = (h || '').replace(/^#/, '');
  let m = /^issue-([a-z]+-\d+)(?:-(runs|children|activity))?$/.exec(h); if (m && byKey[m[1]]) return { page: 'issue', key: m[1], tab: m[2] };
  m = /^run-([a-z0-9]+?)(?:-([sabc]\d+))?$/.exec(h); if (m) return { page: 'run', id: m[1], step: m[2] };
  m = /^release-(rel-[a-z0-9]+)$/.exec(h); if (m) return { page: 'release', id: m[1] };
  m = /^(board|runs|releases)(?:-(gc|frk|dum))?$/.exec(h); if (m) return { page: m[1], project: m[2] || 'gc' };
  if (h === 'approvals') return { page: 'approvals' };
  return { page: 'board', project: 'gc' };
}
function App() {
  const [route, setRoute] = useState(() => fromHash(location.hash));
  const [project, setProject] = useState(() => route.project ? route.project : route.page === 'issue' ? byKey[route.key].project : route.page === 'run' ? byKey[runFor(route.id).issue].project : 'gc');
  const [palette, setPalette] = useState(false);
  const [drawer, setDrawer] = useState(false);
  const [msg, setMsg] = useState('');
  const [done, setDone] = useState(new Set());
  const [theme, setTheme] = useState(null);
  const mainRef = useRef(null);
  // Every navigation is a hash change, so the browser's back and forward buttons walk the history.
  const nav = (r) => { setDrawer(false); if (['board', 'runs', 'releases'].includes(r.page) && !r.project) r = { ...r, project };
    const h = '#' + toHash(r); if (location.hash !== h) location.hash = h; else setRoute(r); };
  useEffect(() => { const p = route.project || (route.page === 'issue' ? byKey[route.key].project : route.page === 'run' ? byKey[runFor(route.id).issue].project : null);
    if (p) setProject(p); if (mainRef.current) mainRef.current.scrollTop = 0; }, [route]);
  const toast = (m) => { setMsg(m); clearTimeout(window.__t); window.__t = setTimeout(() => setMsg(''), 2800); };
  useEffect(() => { const k = (e) => { if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); } };
    const h = () => setRoute(fromHash(location.hash)); addEventListener('keydown', k); addEventListener('hashchange', h); return () => { removeEventListener('keydown', k); removeEventListener('hashchange', h); }; }, []);
  const dark = theme ? theme === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
  const flip = () => { const t = dark ? 'light' : 'dark'; setTheme(t); document.documentElement.setAttribute('data-theme', t); };
  const waiting = APPROVALS.filter(a => !done.has(a.id)).length;
  const crumbs = crumbsFor(route, project);
  const page = route.page === 'board' ? <Board project={project} nav={nav} toast={toast} />
    : route.page === 'issue' ? <IssuePage k={route.key} tab={route.tab} nav={nav} toast={toast} />
    : route.page === 'run' ? <RunPage id={route.id} step={route.step} nav={nav} toast={toast} />
    : route.page === 'runs' ? <RunsPage project={project} nav={nav} />
    : route.page === 'approvals' ? <ApprovalsPage nav={nav} toast={toast} done={done} setDone={setDone} />
    : route.page === 'releases' ? <ReleasesPage project={project} nav={nav} />
    : <ReleasePage id={route.id} nav={nav} toast={toast} done={done} setDone={setDone} />;
  return <div className="flex h-full">
    <aside className="hidden w-60 shrink-0 border-r border-line md:block"><Sidebar route={route} nav={nav} project={project} setProject={setProject} waiting={waiting} /></aside>
    {drawer && <div className="fixed inset-0 z-30 md:hidden"><div className="absolute inset-0 bg-black/40" onClick={() => setDrawer(false)} /><div className="absolute inset-y-0 left-0 w-64 border-r border-line"><Sidebar route={route} nav={nav} project={project} setProject={setProject} waiting={waiting} onClose={() => setDrawer(false)} /></div></div>}
    <div className="flex min-w-0 flex-1 flex-col">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b border-line bg-bg" style={{ paddingLeft: 'max(16px, env(safe-area-inset-left, 0px))', paddingRight: 'max(16px, env(safe-area-inset-right, 0px))' }}>
        <Button variant="ghost" size="icon" className="md:hidden" onClick={() => setDrawer(true)} aria-label="open menu"><Icon name="menu" /></Button>
        <ol className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden text-[13px]">{crumbs.map(([label, r], n) => <li key={n} className={cx('flex min-w-0 items-center gap-1', n < crumbs.length - 2 && 'hidden sm:flex')}>
          {n > 0 && <Icon name="right" className="h-3.5 w-3.5 shrink-0 text-muted-fg" />}
          {r && n < crumbs.length - 1 ? <button onClick={() => nav(r)} className="truncate text-muted-fg hover:text-fg">{label}</button> : <span className="truncate font-medium">{label}</span>}</li>)}</ol>
        <button onClick={() => setPalette(true)} className="hidden h-8 items-center gap-2 rounded-md border border-line bg-muted px-2.5 text-[13px] text-muted-fg hover:text-fg sm:flex"><Icon name="search" className="h-3.5 w-3.5" />Search<Kbd>⌘K</Kbd></button>
        <Button variant="ghost" size="icon" className="sm:hidden" onClick={() => setPalette(true)} aria-label="search"><Icon name="search" /></Button>
        <Button variant="ghost" size="icon" onClick={flip} aria-label="toggle theme"><Icon name={dark ? 'sun' : 'moon'} /></Button>
      </header>
      <main ref={mainRef} className="min-h-0 flex-1 overflow-y-auto scroll-thin">
        <div className="mx-auto max-w-6xl py-6" style={{ paddingLeft: 'max(16px, env(safe-area-inset-left, 0px))', paddingRight: 'max(16px, env(safe-area-inset-right, 0px))', paddingBottom: 'calc(24px + env(safe-area-inset-bottom, 0px))' }}>{page}</div>
      </main>
    </div>
    {palette && <Palette onClose={() => setPalette(false)} nav={nav} project={project} />}
    <Toast msg={msg} />
  </div>;
}
ReactDOM.createRoot(document.getElementById('root')).render(<App />);
