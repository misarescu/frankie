-- Verification scaffolding for verify-frankie: rows for the project `verify/demo`
-- (key prefix vfy), which the control plane creates from its config at startup.
-- The control plane runs without Temporal here, so nothing advances these rows
-- except what a person does in the UI. Applied once by `frankie-verify launch`
-- to a throwaway Postgres; never run it against a real database.
BEGIN;

-- Issue ids: 00000000-0000-7000-8000-0000000001NN is vfy-NN.
INSERT INTO issues (id, project_id, number, key, parent_id, type, title, spec, acceptance_criteria, status, created_by, labels,
                    start_requested_at, start_requested_by, spec_proposal)
SELECT v.id::uuid, p.id, v.num, 'vfy-' || v.num, v.parent::uuid, v.type, v.title, v.spec, v.ac::text[], v.status, 'verify-seed', '{}',
       v.req_at, v.req_by, v.proposal::jsonb
FROM projects p, (VALUES
  ('00000000-0000-7000-8000-000000000101', 1, NULL, 'epic', 'Todo service', 'A small HTTP todo service.', '{}', 'ready', NULL::timestamptz, NULL, NULL),
  ('00000000-0000-7000-8000-000000000102', 2, '00000000-0000-7000-8000-000000000101', 'task', 'List todos endpoint', 'GET /todos returns every todo.', '{"returns 200 with a JSON array"}', 'ready', NULL, NULL, NULL),
  ('00000000-0000-7000-8000-000000000103', 3, '00000000-0000-7000-8000-000000000101', 'task', 'Create todo endpoint', 'POST /todos stores a todo.', '{"returns 201 with the stored todo"}', 'ready', NULL, NULL, NULL),
  ('00000000-0000-7000-8000-000000000104', 4, NULL, 'bug', 'Pagination returns duplicates', 'Page 2 repeats the last item of page 1.', '{"no item appears on two pages"}', 'running', NULL, NULL, NULL),
  ('00000000-0000-7000-8000-000000000105', 5, '00000000-0000-7000-8000-000000000101', 'story', 'Todo tags', 'Let a todo carry tags.', '{}', 'refining', now(), 'verify-seed',
     '{"spec":"Add a tags array to a todo, stored and returned by both endpoints.","acceptance_criteria":["a todo created with tags returns them","a todo without tags returns an empty array"],"file_hints":["internal/todo/todo.go"],"summary":"Refined: tags are a plain string array."}'),
  ('00000000-0000-7000-8000-000000000106', 6, NULL, 'task', 'Add a health endpoint', 'GET /healthz answers ok.', '{"returns 200"}', 'awaiting_human', NULL, NULL, NULL),
  ('00000000-0000-7000-8000-000000000107', 7, NULL, 'task', 'Project skeleton', 'Go module and main package.', '{}', 'merged', NULL, NULL, NULL)
) AS v(id, num, parent, type, title, spec, ac, status, req_at, req_by, proposal)
WHERE p.key_prefix = 'vfy';

UPDATE projects SET next_issue_number = 8 WHERE key_prefix = 'vfy';

INSERT INTO issue_dependencies (issue_id, depends_on_id) VALUES
  ('00000000-0000-7000-8000-000000000103', '00000000-0000-7000-8000-000000000102');

-- Runs: ...02NN. A running bug-fix run (vfy-4), a human gate waiting (vfy-6) and
-- a finished run (vfy-7).
INSERT INTO runs (id, issue_id, workflow_snapshot, workflow_version, temporal_workflow_id, temporal_run_id, status, started_at, finished_at)
VALUES
  ('00000000-0000-7000-8000-000000000204', '00000000-0000-7000-8000-000000000104',
   '{"workflow":{"name":"bug-fix","version":1,"steps":[{"id":"investigate","agent":{"role":"investigator","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"fix","agent":{"role":"coder","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"checks","gate":{"type":"command","run":["mise run test"]}}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}',
   'bug-fix@1', 'issue-vfy-4', 'verify-run-vfy-4', 'running', now() - interval '10 minutes', NULL),
  ('00000000-0000-7000-8000-000000000206', '00000000-0000-7000-8000-000000000106',
   '{"workflow":{"name":"default-build","version":1,"steps":[{"id":"implement","agent":{"role":"coder","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"approve","gate":{"type":"human"}}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}',
   'default-build@1', 'issue-vfy-6', 'verify-run-vfy-6', 'running', now() - interval '30 minutes', NULL),
  ('00000000-0000-7000-8000-000000000207', '00000000-0000-7000-8000-000000000107',
   '{"workflow":{"name":"default-build","version":1,"steps":[{"id":"implement","agent":{"role":"coder","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"checks","gate":{"type":"command","run":["mise run test"]}}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}',
   'default-build@1', 'issue-vfy-7', 'verify-run-vfy-7', 'succeeded', now() - interval '3 days', now() - interval '3 days' + interval '9 minutes');

-- Step runs: ...03NN.
INSERT INTO step_runs (id, run_id, step_id, attempt, kind, status, commit_sha, cost_usd, input_tokens, output_tokens, started_at, finished_at)
VALUES
  ('00000000-0000-7000-8000-000000000304', '00000000-0000-7000-8000-000000000204', 'investigate', 1, 'agent', 'running', NULL, 0.1234, 18000, 2400, now() - interval '9 minutes', NULL),
  ('00000000-0000-7000-8000-000000000306', '00000000-0000-7000-8000-000000000206', 'implement', 1, 'agent', 'succeeded', 'abc1234abc1234abc1234abc1234abc1234abcd', 0.4100, 52000, 9100, now() - interval '29 minutes', now() - interval '15 minutes'),
  ('00000000-0000-7000-8000-000000000316', '00000000-0000-7000-8000-000000000206', 'approve', 1, 'gate', 'running', 'abc1234abc1234abc1234abc1234abc1234abcd', 0, 0, 0, now() - interval '14 minutes', NULL),
  ('00000000-0000-7000-8000-000000000307', '00000000-0000-7000-8000-000000000207', 'implement', 1, 'agent', 'succeeded', 'def5678def5678def5678def5678def5678def5', 0.2200, 30000, 4000, now() - interval '3 days', now() - interval '3 days' + interval '6 minutes'),
  ('00000000-0000-7000-8000-000000000317', '00000000-0000-7000-8000-000000000207', 'checks', 1, 'gate', 'succeeded', 'def5678def5678def5678def5678def5678def5', 0, 0, 0, now() - interval '3 days' + interval '6 minutes', now() - interval '3 days' + interval '9 minutes');

-- The human gate vfy-6 waits on, shown in the approvals inbox.
INSERT INTO approvals (id, step_run_id, run_id, reason, commit_sha, change_request_url, status, can_request_changes, expires_at)
VALUES ('00000000-0000-7000-8000-000000000406', '00000000-0000-7000-8000-000000000316', '00000000-0000-7000-8000-000000000206',
        'Protected paths changed: .golangci.yml', 'abc1234abc1234abc1234abc1234abc1234abcd',
        'https://example.invalid/verify/demo/pull/6', 'pending', true, now() + interval '23 hours');

-- Transcripts (what the run pages show).
INSERT INTO events (id, run_id, step_run_id, ts, type, payload) VALUES
  ('00000000-0000-7000-8000-000000000501', '00000000-0000-7000-8000-000000000204', '00000000-0000-7000-8000-000000000304', now() - interval '9 minutes', 'assistant_message', '{"text":"Reading the pagination code to find where page 2 starts."}'),
  ('00000000-0000-7000-8000-000000000502', '00000000-0000-7000-8000-000000000204', '00000000-0000-7000-8000-000000000304', now() - interval '8 minutes', 'tool_call', '{"name":"Grep","input":{"pattern":"offset"}}'),
  ('00000000-0000-7000-8000-000000000503', '00000000-0000-7000-8000-000000000204', '00000000-0000-7000-8000-000000000304', now() - interval '8 minutes', 'tool_result', '{"content":"internal/todo/page.go:14: start := page * size"}'),
  ('00000000-0000-7000-8000-000000000504', '00000000-0000-7000-8000-000000000204', '00000000-0000-7000-8000-000000000304', now() - interval '7 minutes', 'token_usage', '{"cost_usd":0.1234,"turns":6,"output_tokens":2400}'),
  ('00000000-0000-7000-8000-000000000511', '00000000-0000-7000-8000-000000000207', NULL, now() - interval '3 days', 'state_change', '{"entity":"run","from":"pending","to":"running"}'),
  ('00000000-0000-7000-8000-000000000512', '00000000-0000-7000-8000-000000000207', '00000000-0000-7000-8000-000000000307', now() - interval '3 days', 'assistant_message', '{"text":"Creating the Go module and the main package."}'),
  ('00000000-0000-7000-8000-000000000513', '00000000-0000-7000-8000-000000000207', '00000000-0000-7000-8000-000000000307', now() - interval '3 days', 'command_output', '{"output":"go mod init example.com/todo"}'),
  ('00000000-0000-7000-8000-000000000514', '00000000-0000-7000-8000-000000000207', '00000000-0000-7000-8000-000000000307', now() - interval '3 days', 'done', '{"status":"succeeded","commit_sha":"def5678"}'),
  ('00000000-0000-7000-8000-000000000515', '00000000-0000-7000-8000-000000000207', '00000000-0000-7000-8000-000000000317', now() - interval '3 days', 'gate_result', '{"type":"command","verdict":"pass","details":{"commands":[{"command":"mise run test","exitCode":0}]}}'),
  ('00000000-0000-7000-8000-000000000516', '00000000-0000-7000-8000-000000000207', NULL, now() - interval '3 days', 'state_change', '{"entity":"run","from":"running","to":"succeeded"}');

-- A release of the merged issue: one stage that passed and one still waiting.
INSERT INTO releases (id, project_id, repo, sha, status, bump, notes, notes_source, version, finished_at)
SELECT '00000000-0000-7000-8000-000000000601', p.id, 'verify/demo', '1234567890abcdef1234567890abcdef12345678', 'succeeded', 'minor',
       E'## v0.1.0\n\n- Project skeleton (vfy-7)', 'agent', 'v0.1.0', now() - interval '2 days'
FROM projects p WHERE p.key_prefix = 'vfy';

INSERT INTO release_issues (release_id, issue_id, position)
VALUES ('00000000-0000-7000-8000-000000000601', '00000000-0000-7000-8000-000000000107', 1);

INSERT INTO deployments (id, release_id, stage, driver, status, reason, verification, started_at, finished_at)
VALUES ('00000000-0000-7000-8000-000000000701', '00000000-0000-7000-8000-000000000601', 'staging', 'flux', 'succeeded', '',
        '{"passed":true,"summary":"12 of 12 requests passed"}', now() - interval '2 days' - interval '5 minutes', now() - interval '2 days'),
       ('00000000-0000-7000-8000-000000000702', '00000000-0000-7000-8000-000000000601', 'production', 'flux', 'pending', '', NULL, NULL, NULL);

UPDATE runs SET created_at = started_at;
UPDATE releases SET created_at = finished_at - interval '10 minutes';

COMMIT;
