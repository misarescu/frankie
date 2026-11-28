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

-- A second release, waiting at its production gate (D139): staging passed, the
-- release run holds a gate step named after the stage and its pending approval,
-- and production has no deployment row until someone approves.
INSERT INTO releases (id, project_id, repo, sha, status, bump, created_at)
SELECT '00000000-0000-7000-8000-000000000602', p.id, 'verify/demo', 'fedcba9876543210fedcba9876543210fedcba98', 'in_progress', 'patch',
       now() - interval '1 hour'
FROM projects p WHERE p.key_prefix = 'vfy';

INSERT INTO release_issues (release_id, issue_id, position)
VALUES ('00000000-0000-7000-8000-000000000602', '00000000-0000-7000-8000-000000000107', 1);

INSERT INTO deployments (id, release_id, stage, driver, status, reason, verification, started_at, finished_at)
VALUES ('00000000-0000-7000-8000-000000000703', '00000000-0000-7000-8000-000000000602', 'staging', 'flux', 'succeeded', '',
        '{"passed":true,"summary":"12 of 12 requests passed"}', now() - interval '55 minutes', now() - interval '50 minutes');

INSERT INTO runs (id, release_id, workflow_snapshot, workflow_version, temporal_workflow_id, temporal_run_id, status, started_at)
VALUES ('00000000-0000-7000-8000-000000000208', '00000000-0000-7000-8000-000000000602', '{}', 'release-agent',
        'release/verify/demo', 'verify-release-fedcba9', 'running', now() - interval '1 hour');

INSERT INTO step_runs (id, run_id, step_id, attempt, kind, status, commit_sha, started_at)
VALUES ('00000000-0000-7000-8000-000000000318', '00000000-0000-7000-8000-000000000208', 'production', 1, 'gate', 'running',
        'fedcba9876543210fedcba9876543210fedcba98', now() - interval '50 minutes');

INSERT INTO approvals (id, step_run_id, run_id, reason, commit_sha, status, can_request_changes, expires_at)
VALUES ('00000000-0000-7000-8000-000000000408', '00000000-0000-7000-8000-000000000318', '00000000-0000-7000-8000-000000000208',
        'Release fedcba9 of verify/demo passed the stages before production. Approve to deploy it to production, or reject to stop the release before it.',
        'fedcba9876543210fedcba9876543210fedcba98', 'pending', false, now() + interval '71 hours');

-- ===== M9 edge cases (vfy-8..vfy-18 and project verify/other) =====
-- An epic vfy-8 > stories vfy-9, vfy-10 > tasks and a bug, with costs at
-- every level so totals (D142) can be checked; every step kind and event
-- type; a waiting question; a failed run; a 200-character title and a long
-- spec (vfy-14); overlaps (vfy-11, vfy-12 and vfy-14 share web/src/signup);
-- a rolled-back release and one deploying.
INSERT INTO issues (id, project_id, number, key, parent_id, type, title, spec, acceptance_criteria, status, created_by, labels, start_requested_at, start_requested_by, spec_approved_at, spec_approved_by, file_hints, changed_paths, source_ref, closed_at, closed_by, close_reason)
SELECT v.id::uuid, p.id, v.num, 'vfy-' || v.num, v.parent::uuid, v.type, v.title, v.spec, v.ac, v.status, v.created_by, v.labels, v.start_requested_at, v.start_requested_by, v.spec_approved_at, v.spec_approved_by, v.file_hints, v.changed_paths, v.source_ref, v.closed_at, v.closed_by, v.close_reason
FROM projects p, (VALUES
  ('00000000-0000-7000-8000-000000000108', 8, NULL, 'epic', 'Accounts', 'People sign up, sign in and keep a profile.', '{}'::text[], 'ready', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000000109', 9, '00000000-0000-7000-8000-000000000108', 'story', 'Sign-up', 'A person creates an account with an email and a password.', '{"a new account can sign in"}'::text[], 'ready', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, (now() - interval '2 days'), 'verifier', '{}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000000110', 10, '00000000-0000-7000-8000-000000000108', 'story', 'Profile', 'A person sees and edits their profile.', '{}'::text[], 'ready', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000000111', 11, '00000000-0000-7000-8000-000000000109', 'task', 'Sign-up form', 'The form with email, name and password, and its errors.', '{"an empty email shows \"Enter an email address\""}'::text[], 'running', 'verify-seed', '{}'::text[], (now() - interval '61 minutes'), 'verifier', NULL::timestamptz, NULL, '{"web/src/signup/form.tsx"}'::text[], '{"web/src/signup/form.tsx","web/src/signup/form.test.tsx"}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000000112', 12, '00000000-0000-7000-8000-000000000109', 'task', 'Email confirmation', 'Send a confirmation link after sign-up.', '{"the link confirms the account once"}'::text[], 'running', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{"web/src/signup/confirm.ts"}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000000113', 13, '00000000-0000-7000-8000-000000000110', 'bug', 'Avatar upload crashes on PNG files', 'Opened by release b0b0b0b: profile/upload avatar png failed at staging.', '{"a PNG avatar uploads"}'::text[], 'failed', 'release:b0b0b0b', '{"bug","release"}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{}'::text[], '{}'::text[], 'https://example.invalid/verify/demo/issues/13', NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000000114', 14, '00000000-0000-7000-8000-000000000110', 'task', 'Validate every sign-up field on the client and on the server, show each error next to its field in plain words, keep what the person typed after a failed submit, and never reveal if an email is taken.', 'Sign-up today accepts anything and fails late, on the server, with one generic message at the top of the form. People retype everything after an error and cannot tell which field was wrong.

## What changes

- Every field is validated as the person leaves it and again on submit: email (RFC 5322 address, at most 254 characters), display name (1 to 64 characters, no control characters), password (at least 12 characters, not one of the 10,000 most common).
- The server repeats the same checks and answers 422 with one error per field: `{"errors": {"email": "Enter an email address"}}`.
- Each error appears under its field, in plain words, and is announced to screen readers (`role="alert"`).
- After a failed submit the form keeps every value except the password.
- An email that is already registered gets the same answer as a new one ("Check your inbox"), so the form never reveals who has an account.

## Out of scope

- Social sign-in (vfy-16, canceled).
- Password reset (vfy-18).

## Notes

The validation rules live in one module shared by the client and the server:

```ts
export const rules = {
  email: (v: string) => (/^[^@\s]+@[^@\s]+$/.test(v) && v.length <= 254 ? null : "Enter an email address"),
  name: (v: string) => (v.trim().length >= 1 && v.length <= 64 ? null : "Enter a name of up to 64 characters"),
  password: (v: string) => (v.length >= 12 ? null : "Use at least 12 characters"),
};
```

A very long unbroken token, to check that the page wraps it instead of scrolling sideways: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6

1. Step 1 of the rollout: ship the rule for field group 1 behind the form, measure the error rate for a day, then move on.
2. Step 2 of the rollout: ship the rule for field group 2 behind the form, measure the error rate for a day, then move on.
3. Step 3 of the rollout: ship the rule for field group 3 behind the form, measure the error rate for a day, then move on.
4. Step 4 of the rollout: ship the rule for field group 4 behind the form, measure the error rate for a day, then move on.
5. Step 5 of the rollout: ship the rule for field group 5 behind the form, measure the error rate for a day, then move on.
6. Step 6 of the rollout: ship the rule for field group 6 behind the form, measure the error rate for a day, then move on.
7. Step 7 of the rollout: ship the rule for field group 7 behind the form, measure the error rate for a day, then move on.
8. Step 8 of the rollout: ship the rule for field group 8 behind the form, measure the error rate for a day, then move on.
9. Step 9 of the rollout: ship the rule for field group 9 behind the form, measure the error rate for a day, then move on.
10. Step 10 of the rollout: ship the rule for field group 10 behind the form, measure the error rate for a day, then move on.
11. Step 11 of the rollout: ship the rule for field group 11 behind the form, measure the error rate for a day, then move on.
12. Step 12 of the rollout: ship the rule for field group 12 behind the form, measure the error rate for a day, then move on.', '{"each field shows its own error under it","errors are announced to screen readers","the server answers 422 with one error per field","a failed submit keeps every value except the password","a taken email gets the same answer as a new one","the client and the server use the same rules module","an email of 255 characters is rejected","a password from the common list is rejected"}'::text[], 'ready', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{"web/src/signup/validate.ts","internal/accounts/validate.go"}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000000115', 15, '00000000-0000-7000-8000-000000000110', 'task', 'Profile page', 'Show the name, email and avatar; let the person change them.', '{"the page shows the saved avatar"}'::text[], 'merged', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{}'::text[], '{"web/src/profile/page.tsx","internal/profile/avatar.go"}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000000116', 16, '00000000-0000-7000-8000-000000000109', 'task', 'Social sign-in', 'Sign in with GitHub or Google.', '{}'::text[], 'canceled', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{}'::text[], '{}'::text[], NULL, (now() - interval '23 hours'), 'verifier', 'Out of scope for the first release.'),
  ('00000000-0000-7000-8000-000000000117', 17, NULL, 'task', 'Rate-limit sign-in attempts', 'At most 5 failed attempts per minute per address.', '{"the sixth attempt within a minute gets 429"}'::text[], 'queued', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{"internal/auth/ratelimit.go"}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000000118', 18, NULL, 'task', 'Password reset', 'Reset a forgotten password by email.', '{}'::text[], 'draft', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, '')
) AS v(id, num, parent, type, title, spec, ac, status, created_by, labels, start_requested_at, start_requested_by, spec_approved_at, spec_approved_by, file_hints, changed_paths, source_ref, closed_at, closed_by, close_reason)
WHERE p.key_prefix = 'vfy';

-- A second project, so the project switcher has two.
INSERT INTO issues (id, project_id, number, key, parent_id, type, title, spec, acceptance_criteria, status, created_by, labels, start_requested_at, start_requested_by, spec_approved_at, spec_approved_by, file_hints, changed_paths, source_ref, closed_at, closed_by, close_reason)
SELECT v.id::uuid, p.id, v.num, 'oth-' || v.num, v.parent::uuid, v.type, v.title, v.spec, v.ac, v.status, v.created_by, v.labels, v.start_requested_at, v.start_requested_by, v.spec_approved_at, v.spec_approved_by, v.file_hints, v.changed_paths, v.source_ref, v.closed_at, v.closed_by, v.close_reason
FROM projects p, (VALUES
  ('00000000-0000-7000-8000-000000001401', 1, NULL, 'epic', 'Billing', 'Invoices and taxes for paid plans.', '{}'::text[], 'ready', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000001402', 2, '00000000-0000-7000-8000-000000001401', 'task', 'Invoice PDF', 'Render each invoice as a PDF.', '{"the PDF shows every line item"}'::text[], 'ready', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, ''),
  ('00000000-0000-7000-8000-000000001403', 3, '00000000-0000-7000-8000-000000001401', 'task', 'Tax rates', 'Apply the tax rate of the customer''s country.', '{}'::text[], 'merged', 'verify-seed', '{}'::text[], NULL::timestamptz, NULL, NULL::timestamptz, NULL, '{}'::text[], '{}'::text[], NULL, NULL::timestamptz, NULL, '')
) AS v(id, num, parent, type, title, spec, ac, status, created_by, labels, start_requested_at, start_requested_by, spec_approved_at, spec_approved_by, file_hints, changed_paths, source_ref, closed_at, closed_by, close_reason)
WHERE p.key_prefix = 'oth';

INSERT INTO issue_dependencies (issue_id, depends_on_id) VALUES
  ('00000000-0000-7000-8000-000000000110', '00000000-0000-7000-8000-000000000109'),
  ('00000000-0000-7000-8000-000000000114', '00000000-0000-7000-8000-000000000113'),
  ('00000000-0000-7000-8000-000000000114', '00000000-0000-7000-8000-000000000115'),
  ('00000000-0000-7000-8000-000000000118', '00000000-0000-7000-8000-000000000111'),
  ('00000000-0000-7000-8000-000000001402', '00000000-0000-7000-8000-000000001403');

UPDATE projects SET next_issue_number = 19 WHERE key_prefix = 'vfy';
UPDATE projects SET next_issue_number = 4 WHERE key_prefix = 'oth';

-- Runs: ...21NN is vfy-NN's (A = vfy-11 looped back, B = vfy-12 waiting for an
-- answer, C = vfy-13 failed, D = vfy-15 through every gate type), ...22NN a
-- refinement of vfy-NN, ...2403 is oth-3's.
INSERT INTO runs (id, issue_id, workflow_snapshot, workflow_version, temporal_workflow_id, temporal_run_id, status, loop_counts, started_at, finished_at)
VALUES
  ('00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000000111', '{"workflow":{"name":"default-build","version":2,"steps":[{"id":"scope","agent":{"role":"scoper","harness":"claude-code","model":"anthropic/claude-haiku-5"}},{"id":"plan","agent":{"role":"planner","harness":"claude-code","model":"anthropic/claude-opus-5-5"},"when":{"step":"scope","field":"needs_plan","equals":true}},{"id":"implement","agent":{"role":"coder","harness":"opencode","model":"openrouter/qwen/qwen3-coder"}},{"id":"checks","gate":{"type":"command","run":["mise run lint","mise run test"],"onFail":{"goto":"implement","maxLoops":3}}},{"id":"review","parallel":[{"id":"review-security","agent":{"role":"security-reviewer","harness":"claude-code"}},{"id":"review-style","agent":{"role":"style-reviewer","harness":"pi"}}],"gate":{"type":"agent-verdict"}},{"id":"approve","gate":{"type":"human"}},{"id":"enqueue","action":"merge-queue.enqueue"}]},"roles":{},"source":".factory/workflows/default-build.yaml","baseSHA":"0000000000000000000000000000000000000001"}', 'default-build@2', 'issue-vfy-11', 'verify-run-issue-vfy-11', 'running', '{"checks":1}', now() - interval '61 minutes', NULL),
  ('00000000-0000-7000-8000-000000002112', '00000000-0000-7000-8000-000000000112', '{"workflow":{"name":"default-build","version":1,"steps":[{"id":"implement","agent":{"role":"coder","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"checks","gate":{"type":"command","run":["mise run test"]}}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}', 'default-build@1', 'issue-vfy-12', 'verify-run-issue-vfy-12', 'running', '{}', now() - interval '26 minutes', NULL),
  ('00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000000113', '{"workflow":{"name":"bug-fix","version":1,"steps":[{"id":"investigate","agent":{"role":"investigator","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"decide","gate":{"type":"human"},"when":{"step":"investigate","field":"testable","equals":false}},{"id":"reproduce","agent":{"role":"test-writer","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"must-fail","gate":{"type":"command","run":["mise run test"],"expect":"fail"}},{"id":"fix","agent":{"role":"coder","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"keep-test","gate":{"type":"preserve","step":"reproduce"}},{"id":"checks","gate":{"type":"command","run":["mise run test"]}},{"id":"enqueue","action":"merge-queue.enqueue"}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}', 'bug-fix@1', 'issue-vfy-13', 'verify-run-issue-vfy-13', 'failed', '{}', now() - interval '301 minutes', now() - interval '277 minutes'),
  ('00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000000115', '{"workflow":{"name":"build-e2e","version":1,"steps":[{"id":"implement","agent":{"role":"coder","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"e2e","agent":{"role":"e2e-writer","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"keep-tests","gate":{"type":"preserve","step":"e2e"}},{"id":"review","gate":{"type":"agent-verdict"}},{"id":"preview","gate":{"type":"preview"}},{"id":"approve","gate":{"type":"human"}},{"id":"ci","gate":{"type":"provider-checks"}},{"id":"enqueue","action":"merge-queue.enqueue"}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}', 'build-e2e@1', 'issue-vfy-15', 'verify-run-issue-vfy-15', 'succeeded', '{}', now() - interval '601 minutes', now() - interval '550 minutes'),
  ('00000000-0000-7000-8000-000000002116', '00000000-0000-7000-8000-000000000116', '{"workflow":{"name":"default-build","version":1,"steps":[{"id":"implement","agent":{"role":"coder","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"checks","gate":{"type":"command","run":["mise run test"]}},{"id":"enqueue","action":"merge-queue.enqueue"}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}', 'default-build@1', 'issue-vfy-16', 'verify-run-issue-vfy-16', 'canceled', '{}', now() - interval '1450 minutes', now() - interval '1430 minutes'),
  ('00000000-0000-7000-8000-000000002117', '00000000-0000-7000-8000-000000000117', '{"workflow":{"name":"default-build","version":1,"steps":[{"id":"implement","agent":{"role":"coder","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"checks","gate":{"type":"command","run":["mise run test"]}},{"id":"enqueue","action":"merge-queue.enqueue"}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}', 'default-build@1', 'issue-vfy-17', 'verify-run-issue-vfy-17', 'succeeded', '{}', now() - interval '130 minutes', now() - interval '100 minutes'),
  ('00000000-0000-7000-8000-000000002208', '00000000-0000-7000-8000-000000000108', '{"workflow":{"name":"refine","version":1,"steps":[{"id":"refine","agent":{"role":"refiner","harness":"claude-code","model":"anthropic/claude-sonnet-5"}}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}', 'refine@1', 'refine-vfy-8', 'verify-run-refine-vfy-8', 'succeeded', '{}', now() - interval '4320 minutes', now() - interval '4310 minutes'),
  ('00000000-0000-7000-8000-000000002209', '00000000-0000-7000-8000-000000000109', '{"workflow":{"name":"refine","version":1,"steps":[{"id":"refine","agent":{"role":"refiner","harness":"claude-code","model":"anthropic/claude-sonnet-5"}}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}', 'refine@1', 'refine-vfy-9', 'verify-run-refine-vfy-9', 'succeeded', '{}', now() - interval '4300 minutes', now() - interval '4295 minutes'),
  ('00000000-0000-7000-8000-000000002403', '00000000-0000-7000-8000-000000001403', '{"workflow":{"name":"default-build","version":1,"steps":[{"id":"implement","agent":{"role":"coder","harness":"claude-code","model":"anthropic/claude-sonnet-5"}},{"id":"checks","gate":{"type":"command","run":["mise run test"]}},{"id":"enqueue","action":"merge-queue.enqueue"}]},"roles":{},"source":"built-in","baseSHA":"0000000000000000000000000000000000000001"}', 'default-build@1', 'issue-oth-3', 'verify-run-issue-oth-3', 'succeeded', '{}', now() - interval '2000 minutes', now() - interval '1990 minutes');

-- A release rolled back at staging after its verification failed, which opened
-- vfy-13 as its bug issue; and a release deploying to staging now.
INSERT INTO releases (id, project_id, repo, sha, status, reason, bug_issue_ref, bump, notes, notes_source, finished_at)
SELECT '00000000-0000-7000-8000-000000000603', p.id, 'verify/demo', 'b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0', 'rolled_back',
       'verification failed at staging: 12 of 14 requests passed; rolled back to v0.1.0',
       'https://example.invalid/verify/demo/issues/13', 'minor', E'## v0.2.0\n\n- Profile page (vfy-15)', 'agent', now() - interval '300 minutes'
FROM projects p WHERE p.key_prefix = 'vfy';
INSERT INTO releases (id, project_id, repo, sha, status, bump, created_at)
SELECT '00000000-0000-7000-8000-000000000604', p.id, 'verify/demo', 'c0ffeec0ffeec0ffeec0ffeec0ffeec0ffeec0ff', 'in_progress', 'patch', now() - interval '8 minutes'
FROM projects p WHERE p.key_prefix = 'vfy';

INSERT INTO release_issues (release_id, issue_id, position) VALUES
  ('00000000-0000-7000-8000-000000000603', '00000000-0000-7000-8000-000000000115', 1),
  ('00000000-0000-7000-8000-000000000604', '00000000-0000-7000-8000-000000000115', 1);

INSERT INTO deployments (id, release_id, stage, driver, status, reason, verification, started_at, finished_at) VALUES
  ('00000000-0000-7000-8000-000000000704', '00000000-0000-7000-8000-000000000603', 'staging', 'flux', 'rolled_back', 'verification failed; rolled back to v0.1.0',
   '{"passed":false,"summary":"12 of 14 requests passed","failures":[{"test":"profile/upload avatar png","issueKeys":["vfy-15"],"message":"expected status 201, got 500"},{"test":"profile/get avatar","message":"expected status 200, got 404"}]}', now() - interval '304 minutes', now() - interval '300 minutes'),
  ('00000000-0000-7000-8000-000000000705', '00000000-0000-7000-8000-000000000604', 'staging', 'flux', 'in_progress', '', NULL, now() - interval '5 minutes', NULL);

-- The release agent's runs (D129): notes and the failed release's analysis,
-- and notes being written for the release in progress.
INSERT INTO runs (id, release_id, workflow_snapshot, workflow_version, temporal_workflow_id, temporal_run_id, status, started_at, finished_at) VALUES
  ('00000000-0000-7000-8000-000000002303', '00000000-0000-7000-8000-000000000603', '{}', 'release-agent', 'release/verify/demo', 'verify-release-b0b0b0b', 'succeeded', now() - interval '309 minutes', now() - interval '296 minutes'),
  ('00000000-0000-7000-8000-000000002304', '00000000-0000-7000-8000-000000000604', '{}', 'release-agent', 'release/verify/demo', 'verify-release-c0ffeec', 'running', now() - interval '8 minutes', NULL);

-- Step runs: 0000003 NN SS A is step SS, attempt A, of vfy-NN's run (23 and 24:
-- the release agent's runs).
INSERT INTO step_runs (id, run_id, step_id, attempt, kind, status, commit_sha, cost_usd, input_tokens, output_tokens, started_at, finished_at)
VALUES
  ('00000000-0000-7000-8000-000000311011', '00000000-0000-7000-8000-000000002111', 'scope', 1, 'agent', 'succeeded', NULL, 0.05, 4000, 600, now() - interval '60 minutes', now() - interval '57 minutes'),
  ('00000000-0000-7000-8000-000000311031', '00000000-0000-7000-8000-000000002111', 'implement', 1, 'agent', 'succeeded', '1a2b3c4000000000000000000000000000000000', 0.8, 90000, 12000, now() - interval '56 minutes', now() - interval '40 minutes'),
  ('00000000-0000-7000-8000-000000311041', '00000000-0000-7000-8000-000000002111', 'checks', 1, 'gate', 'failed', '1a2b3c4000000000000000000000000000000000', 0, 0, 0, now() - interval '40 minutes', now() - interval '36 minutes'),
  ('00000000-0000-7000-8000-000000311032', '00000000-0000-7000-8000-000000002111', 'implement', 2, 'agent', 'succeeded', '5d6e7f8000000000000000000000000000000000', 0.65, 70000, 9000, now() - interval '35 minutes', now() - interval '20 minutes'),
  ('00000000-0000-7000-8000-000000311042', '00000000-0000-7000-8000-000000002111', 'checks', 2, 'gate', 'succeeded', '5d6e7f8000000000000000000000000000000000', 0, 0, 0, now() - interval '20 minutes', now() - interval '16 minutes'),
  ('00000000-0000-7000-8000-000000311051', '00000000-0000-7000-8000-000000002111', 'review-security', 1, 'agent', 'succeeded', '5d6e7f8000000000000000000000000000000000', 0.06, 8000, 800, now() - interval '16 minutes', now() - interval '12 minutes'),
  ('00000000-0000-7000-8000-000000311061', '00000000-0000-7000-8000-000000002111', 'review-style', 1, 'agent', 'running', '5d6e7f8000000000000000000000000000000000', 0.04, 5000, 400, now() - interval '12 minutes', NULL),
  ('00000000-0000-7000-8000-000000312011', '00000000-0000-7000-8000-000000002112', 'implement', 1, 'agent', 'running', NULL, 0.25, 21000, 3100, now() - interval '25 minutes', NULL),
  ('00000000-0000-7000-8000-000000313011', '00000000-0000-7000-8000-000000002113', 'investigate', 1, 'agent', 'succeeded', NULL, 0.3, 40000, 5200, now() - interval '300 minutes', now() - interval '294 minutes'),
  ('00000000-0000-7000-8000-000000313031', '00000000-0000-7000-8000-000000002113', 'reproduce', 1, 'agent', 'succeeded', '9e8d7c6000000000000000000000000000000000', 0.2, 26000, 2600, now() - interval '293 minutes', now() - interval '286 minutes'),
  ('00000000-0000-7000-8000-000000313041', '00000000-0000-7000-8000-000000002113', 'must-fail', 1, 'gate', 'succeeded', '9e8d7c6000000000000000000000000000000000', 0, 0, 0, now() - interval '285 minutes', now() - interval '282 minutes'),
  ('00000000-0000-7000-8000-000000313051', '00000000-0000-7000-8000-000000002113', 'fix', 1, 'agent', 'failed', NULL, 0.15, 15000, 1900, now() - interval '281 minutes', now() - interval '277 minutes'),
  ('00000000-0000-7000-8000-000000315011', '00000000-0000-7000-8000-000000002115', 'implement', 1, 'agent', 'succeeded', 'aa11bb2000000000000000000000000000000000', 0.5, 60000, 8000, now() - interval '600 minutes', now() - interval '590 minutes'),
  ('00000000-0000-7000-8000-000000315021', '00000000-0000-7000-8000-000000002115', 'e2e', 1, 'agent', 'succeeded', 'cc33dd4000000000000000000000000000000000', 0.25, 30000, 4000, now() - interval '590 minutes', now() - interval '585 minutes'),
  ('00000000-0000-7000-8000-000000315031', '00000000-0000-7000-8000-000000002115', 'keep-tests', 1, 'gate', 'succeeded', 'cc33dd4000000000000000000000000000000000', 0, 0, 0, now() - interval '585 minutes', now() - interval '584 minutes'),
  ('00000000-0000-7000-8000-000000315041', '00000000-0000-7000-8000-000000002115', 'review', 1, 'gate', 'succeeded', 'cc33dd4000000000000000000000000000000000', 0.15, 20000, 2000, now() - interval '584 minutes', now() - interval '580 minutes'),
  ('00000000-0000-7000-8000-000000315051', '00000000-0000-7000-8000-000000002115', 'preview', 1, 'gate', 'succeeded', 'cc33dd4000000000000000000000000000000000', 0, 0, 0, now() - interval '580 minutes', now() - interval '575 minutes'),
  ('00000000-0000-7000-8000-000000315061', '00000000-0000-7000-8000-000000002115', 'approve', 1, 'gate', 'succeeded', 'cc33dd4000000000000000000000000000000000', 0, 0, 0, now() - interval '575 minutes', now() - interval '560 minutes'),
  ('00000000-0000-7000-8000-000000315071', '00000000-0000-7000-8000-000000002115', 'ci', 1, 'gate', 'succeeded', 'cc33dd4000000000000000000000000000000000', 0, 0, 0, now() - interval '560 minutes', now() - interval '555 minutes'),
  ('00000000-0000-7000-8000-000000315081', '00000000-0000-7000-8000-000000002115', 'enqueue', 1, 'action', 'succeeded', 'cc33dd4000000000000000000000000000000000', 0, 0, 0, now() - interval '555 minutes', now() - interval '550 minutes'),
  ('00000000-0000-7000-8000-000000316011', '00000000-0000-7000-8000-000000002116', 'implement', 1, 'agent', 'canceled', NULL, 0.4, 40000, 5000, now() - interval '1450 minutes', now() - interval '1430 minutes'),
  ('00000000-0000-7000-8000-000000317011', '00000000-0000-7000-8000-000000002117', 'implement', 1, 'agent', 'succeeded', 'ee55ff6000000000000000000000000000000000', 0.22, 25000, 3000, now() - interval '130 minutes', now() - interval '115 minutes'),
  ('00000000-0000-7000-8000-000000317021', '00000000-0000-7000-8000-000000002117', 'checks', 1, 'gate', 'succeeded', 'ee55ff6000000000000000000000000000000000', 0, 0, 0, now() - interval '115 minutes', now() - interval '105 minutes'),
  ('00000000-0000-7000-8000-000000317031', '00000000-0000-7000-8000-000000002117', 'enqueue', 1, 'action', 'succeeded', 'ee55ff6000000000000000000000000000000000', 0, 0, 0, now() - interval '105 minutes', now() - interval '100 minutes'),
  ('00000000-0000-7000-8000-000000308011', '00000000-0000-7000-8000-000000002208', 'refine', 1, 'agent', 'succeeded', NULL, 0.35, 30000, 4000, now() - interval '4320 minutes', now() - interval '4310 minutes'),
  ('00000000-0000-7000-8000-000000309011', '00000000-0000-7000-8000-000000002209', 'refine', 1, 'agent', 'succeeded', NULL, 0.12, 12000, 1500, now() - interval '4300 minutes', now() - interval '4295 minutes'),
  ('00000000-0000-7000-8000-000000340301', '00000000-0000-7000-8000-000000002403', 'implement', 1, 'agent', 'succeeded', NULL, 0.18, 20000, 2500, now() - interval '2000 minutes', now() - interval '1990 minutes'),
  ('00000000-0000-7000-8000-000000323011', '00000000-0000-7000-8000-000000002303', 'notes', 1, 'agent', 'succeeded', NULL, 0.08, 9000, 1200, now() - interval '309 minutes', now() - interval '306 minutes'),
  ('00000000-0000-7000-8000-000000323021', '00000000-0000-7000-8000-000000002303', 'analysis', 1, 'agent', 'succeeded', NULL, 0.11, 14000, 1600, now() - interval '299 minutes', now() - interval '296 minutes'),
  ('00000000-0000-7000-8000-000000324011', '00000000-0000-7000-8000-000000002304', 'notes', 1, 'agent', 'running', NULL, 0.02, 3000, 200, now() - interval '7 minutes', NULL);

-- vfy-15's human gate, decided (not in the inbox).
INSERT INTO approvals (id, step_run_id, run_id, reason, commit_sha, change_request_url, status, can_request_changes, decided_by, comment, expires_at, decided_at)
VALUES ('00000000-0000-7000-8000-000000000415', '00000000-0000-7000-8000-000000315061', '00000000-0000-7000-8000-000000002115', 'Merge approval required (needs-merge-approval)',
        'cc33dd4' || repeat('0', 33), 'https://example.invalid/verify/demo/pull/15', 'approved', true, 'verifier', 'Looks good.', now() - interval '-1440 minutes', now() - interval '560 minutes');

-- Questions (D55): vfy-11's, answered; vfy-12's, waiting (GET /api/questions lists it).
INSERT INTO questions (id, step_run_id, run_id, question_id, question, status, answer, answered_by, answered_at, created_at) VALUES
  ('00000000-0000-7000-8000-000000000911', '00000000-0000-7000-8000-000000311031', '00000000-0000-7000-8000-000000002111', 'q-vfy-11-1', 'Should the password field show its strength while the person types, or only after submit?',
   'answered', 'Only after submit; keep the form quiet while typing.', 'verifier', now() - interval '46 minutes', now() - interval '48 minutes'),
  ('00000000-0000-7000-8000-000000000912', '00000000-0000-7000-8000-000000312011', '00000000-0000-7000-8000-000000002112', 'q-vfy-12-1', 'Which sender address should confirmation emails use: no-reply@example.invalid or accounts@example.invalid?',
   'pending', NULL, NULL, NULL, now() - interval '17 minutes');

INSERT INTO change_requests (id, issue_id, run_id, repo, number, provider_ref, branch, head_sha, status, merge_sha, merged_at, created_at) VALUES
  ('00000000-0000-7000-8000-000000000811', '00000000-0000-7000-8000-000000000111', '00000000-0000-7000-8000-000000002111', 'verify/demo', 11, 'https://example.invalid/verify/demo/pull/11', 'agent/vfy-11/sign-up-form', '5d6e7f8' || repeat('0', 33), 'open', NULL, NULL, now() - interval '39 minutes'),
  ('00000000-0000-7000-8000-000000000815', '00000000-0000-7000-8000-000000000115', '00000000-0000-7000-8000-000000002115', 'verify/demo', 15, 'https://example.invalid/verify/demo/pull/15', 'agent/vfy-15/profile-page', 'cc33dd4' || repeat('0', 33), 'merged', 'dd44ee5' || repeat('0', 33), now() - interval '550 minutes', now() - interval '589 minutes'),
  ('00000000-0000-7000-8000-000000000817', '00000000-0000-7000-8000-000000000117', '00000000-0000-7000-8000-000000002117', 'verify/demo', 17, 'https://example.invalid/verify/demo/pull/17', 'agent/vfy-17/rate-limit-sign-in', 'ee55ff6' || repeat('0', 33), 'open', NULL, NULL, now() - interval '114 minutes');

-- Transcripts: every event type the UI knows (web/src/events.ts) appears at
-- least once; runs A (vfy-11), B (vfy-12), C (vfy-13) and D (vfy-15).
INSERT INTO events (id, run_id, step_run_id, ts, type, payload) VALUES
  ('00000000-0000-7000-8000-000000051101', '00000000-0000-7000-8000-000000002111', NULL, now() - interval '60 minutes', 'state_change', '{"entity":"run","from":"pending","to":"running"}'),
  ('00000000-0000-7000-8000-000000051102', '00000000-0000-7000-8000-000000002111', NULL, now() - interval '60 minutes', 'state_change', '{"entity":"issue","from":"ready","to":"running"}'),
  ('00000000-0000-7000-8000-000000051103', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311011', now() - interval '60 minutes', 'image_build', '{"image":"toolchain","status":"building","tag":"verify-demo-toolchain:3f9c2a1"}'),
  ('00000000-0000-7000-8000-000000051104', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311011', now() - interval '59 minutes', 'build_log', '{"output":"#1 appending layer mise.toml\n#2 installing go 1.25.4, bun 1.3.2\n#3 pushing verify-demo-toolchain:3f9c2a1"}'),
  ('00000000-0000-7000-8000-000000051105', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311011', now() - interval '59 minutes', 'image_build', '{"image":"toolchain","status":"built","tag":"verify-demo-toolchain:3f9c2a1","seconds":48.2}'),
  ('00000000-0000-7000-8000-000000051106', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311011', now() - interval '58 minutes', 'assistant_message', '{"text":"The form is one component with four fields; no plan step is needed."}'),
  ('00000000-0000-7000-8000-000000051107', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311011', now() - interval '57 minutes', 'token_usage', '{"cost_usd":0.05,"turns":2,"output_tokens":600}'),
  ('00000000-0000-7000-8000-000000051108', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311011', now() - interval '57 minutes', 'done', '{"status":"succeeded"}'),
  ('00000000-0000-7000-8000-000000051109', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '56 minutes', 'assistant_message', '{"text":"Starting with the form component and its test."}'),
  ('00000000-0000-7000-8000-000000051110', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '55 minutes', 'tool_call', '{"name":"Read","input":{"file_path":"web/src/signup/form.tsx"}}'),
  ('00000000-0000-7000-8000-000000051111', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '55 minutes', 'tool_result', '{"content":"export function SignupForm() {\n  return <form />;\n}"}'),
  ('00000000-0000-7000-8000-000000051112', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '54 minutes', 'tool_call', '{"name":"Bash","input":{"command":"bun test web/src/signup"}}'),
  ('00000000-0000-7000-8000-000000051113', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '53 minutes', 'command_output', '{"output":"bun test v1.3.2\n\nweb/src/signup/form.test.tsx:\n(fail) SignupForm > shows an error for an empty email\n\n 0 pass\n 1 fail"}'),
  ('00000000-0000-7000-8000-000000051114', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '50 minutes', 'file_diff', '{"path":"web/src/signup/form.tsx","diff":"@@ -1,3 +1,12 @@\n export function SignupForm() {\n-  return <form />;\n+  const [email, setEmail] = useState(\"\");\n+  return (\n+    <form onSubmit={submit}>\n+      <input name=\"email\" value={email} onChange={(e) => setEmail(e.target.value)} />\n+    </form>\n+  );\n }"}'),
  ('00000000-0000-7000-8000-000000051115', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '48 minutes', 'question', '{"question":"Should the password field show its strength while the person types, or only after submit?","question_id":"q-vfy-11-1"}'),
  ('00000000-0000-7000-8000-000000051116', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '46 minutes', 'intervention', '{"action":"answer","by":"verifier","text":"Only after submit; keep the form quiet while typing.","delivered":true}'),
  ('00000000-0000-7000-8000-000000051117', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '42 minutes', 'assistant_message', '{"text":"Strength is checked on submit. Pushed the form."}'),
  ('00000000-0000-7000-8000-000000051118', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '41 minutes', 'token_usage', '{"cost_usd":0.8,"turns":14,"output_tokens":12000}'),
  ('00000000-0000-7000-8000-000000051119', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311031', now() - interval '40 minutes', 'done', '{"status":"succeeded","commit_sha":"1a2b3c4"}'),
  ('00000000-0000-7000-8000-000000051120', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311041', now() - interval '36 minutes', 'gate_result', '{"type":"command","verdict":"fail","details":{"commands":[{"command":"mise run lint","exitCode":0},{"command":"mise run test","exitCode":1}]}}'),
  ('00000000-0000-7000-8000-000000051121', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311041', now() - interval '36 minutes', 'state_change', '{"entity":"step_run","from":"running","to":"failed"}'),
  ('00000000-0000-7000-8000-000000051122', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311032', now() - interval '34 minutes', 'intervention', '{"action":"steer","by":"verifier","text":"The failing test expects the error text \"Enter an email address\".","delivered":true}'),
  ('00000000-0000-7000-8000-000000051123', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311032', now() - interval '33 minutes', 'assistant_message', '{"text":"The checks gate failed on form.test.tsx; fixing the error text."}'),
  ('00000000-0000-7000-8000-000000051124', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311032', now() - interval '28 minutes', 'file_diff', '{"path":"web/src/signup/form.tsx","diff":"@@ -9 +9 @@\n-      {error && <p>invalid</p>}\n+      {error && <p role=\"alert\">Enter an email address</p>}"}'),
  ('00000000-0000-7000-8000-000000051125', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311032', now() - interval '25 minutes', 'command_output', '{"output":"bun test v1.3.2\n\n 4 pass\n 0 fail"}'),
  ('00000000-0000-7000-8000-000000051126', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311032', now() - interval '21 minutes', 'token_usage', '{"cost_usd":0.65,"turns":9,"output_tokens":9000}'),
  ('00000000-0000-7000-8000-000000051127', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311032', now() - interval '20 minutes', 'done', '{"status":"succeeded","commit_sha":"5d6e7f8"}'),
  ('00000000-0000-7000-8000-000000051128', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311042', now() - interval '16 minutes', 'gate_result', '{"type":"command","verdict":"pass","details":{"commands":[{"command":"mise run lint","exitCode":0},{"command":"mise run test","exitCode":0}]}}'),
  ('00000000-0000-7000-8000-000000051129', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311051', now() - interval '15 minutes', 'assistant_message', '{"text":"No secrets in the form, and the password never leaves the request body."}'),
  ('00000000-0000-7000-8000-000000051130', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311051', now() - interval '13 minutes', 'token_usage', '{"cost_usd":0.06,"turns":3,"output_tokens":800}'),
  ('00000000-0000-7000-8000-000000051131', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311051', now() - interval '12 minutes', 'done', '{"status":"succeeded"}'),
  ('00000000-0000-7000-8000-000000051132', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311051', now() - interval '12 minutes', 'intervention', '{"action":"steer","by":"verifier","text":"Also check the confirmation link.","delivered":false}'),
  ('00000000-0000-7000-8000-000000051133', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311061', now() - interval '11 minutes', 'assistant_message', '{"text":"Reviewing naming and component structure."}'),
  ('00000000-0000-7000-8000-000000051134', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311061', now() - interval '10 minutes', 'tool_call', '{"name":"Grep","input":{"pattern":"useState\\(","path":"web/src/signup"}}'),
  ('00000000-0000-7000-8000-000000051135', '00000000-0000-7000-8000-000000002111', '00000000-0000-7000-8000-000000311061', now() - interval '10 minutes', 'tool_result', '{"content":"web/src/signup/form.tsx:3:  const [email, setEmail] = useState(\"\");"}'),
  ('00000000-0000-7000-8000-000000051201', '00000000-0000-7000-8000-000000002112', NULL, now() - interval '25 minutes', 'state_change', '{"entity":"run","from":"pending","to":"running"}'),
  ('00000000-0000-7000-8000-000000051202', '00000000-0000-7000-8000-000000002112', '00000000-0000-7000-8000-000000312011', now() - interval '25 minutes', 'image_build', '{"image":"dependencies","status":"building","tag":"verify-demo-deps:77aa01b"}'),
  ('00000000-0000-7000-8000-000000051203', '00000000-0000-7000-8000-000000002112', '00000000-0000-7000-8000-000000312011', now() - interval '23 minutes', 'image_build', '{"image":"dependencies","status":"built","tag":"verify-demo-deps:77aa01b","seconds":71}'),
  ('00000000-0000-7000-8000-000000051204', '00000000-0000-7000-8000-000000002112', '00000000-0000-7000-8000-000000312011', now() - interval '20 minutes', 'assistant_message', '{"text":"The confirmation email needs a sender address; the spec does not name one."}'),
  ('00000000-0000-7000-8000-000000051205', '00000000-0000-7000-8000-000000002112', '00000000-0000-7000-8000-000000312011', now() - interval '18 minutes', 'token_usage', '{"cost_usd":0.25,"turns":5,"output_tokens":3100}'),
  ('00000000-0000-7000-8000-000000051206', '00000000-0000-7000-8000-000000002112', '00000000-0000-7000-8000-000000312011', now() - interval '17 minutes', 'question', '{"question":"Which sender address should confirmation emails use: no-reply@example.invalid or accounts@example.invalid?","question_id":"q-vfy-12-1"}'),
  ('00000000-0000-7000-8000-000000051301', '00000000-0000-7000-8000-000000002113', NULL, now() - interval '300 minutes', 'state_change', '{"entity":"run","from":"pending","to":"running"}'),
  ('00000000-0000-7000-8000-000000051302', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313011', now() - interval '299 minutes', 'assistant_message', '{"text":"Reading the upload handler."}'),
  ('00000000-0000-7000-8000-000000051303', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313011', now() - interval '295 minutes', 'findings', '{"findings":{"root_cause":"image.Decode is called without registering the PNG decoder, so png uploads return an error that the handler turns into a panic.","confidence":"high","testable":true}}'),
  ('00000000-0000-7000-8000-000000051304', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313011', now() - interval '294 minutes', 'token_usage', '{"cost_usd":0.3,"turns":8,"output_tokens":5200}'),
  ('00000000-0000-7000-8000-000000051305', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313011', now() - interval '294 minutes', 'done', '{"status":"succeeded"}'),
  ('00000000-0000-7000-8000-000000051306', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313031', now() - interval '290 minutes', 'file_diff', '{"path":"internal/profile/avatar_test.go","diff":"@@ +1,9 @@\n+func TestUploadPNG(t *testing.T) {\n+\tif _, err := Upload(pngFixture); err != nil {\n+\t\tt.Fatal(err)\n+\t}\n+}"}'),
  ('00000000-0000-7000-8000-000000051307', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313031', now() - interval '286 minutes', 'token_usage', '{"cost_usd":0.2,"turns":4,"output_tokens":2600}'),
  ('00000000-0000-7000-8000-000000051308', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313031', now() - interval '286 minutes', 'done', '{"status":"succeeded","commit_sha":"9e8d7c6"}'),
  ('00000000-0000-7000-8000-000000051309', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313041', now() - interval '282 minutes', 'gate_result', '{"type":"command","verdict":"pass","details":{"expect_fail":true,"commands":[{"command":"mise run test","exitCode":1}]}}'),
  ('00000000-0000-7000-8000-000000051310', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313051', now() - interval '280 minutes', 'assistant_message', '{"text":"Rewriting the avatar pipeline around a new image library."}'),
  ('00000000-0000-7000-8000-000000051311', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313051', now() - interval '278 minutes', 'token_usage', '{"cost_usd":0.15,"turns":3,"output_tokens":1900}'),
  ('00000000-0000-7000-8000-000000051312', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313051', now() - interval '277 minutes', 'intervention', '{"action":"abort","by":"verifier","text":"Wrong approach: register the PNG decoder instead of replacing the library.","delivered":true}'),
  ('00000000-0000-7000-8000-000000051313', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313051', now() - interval '277 minutes', 'error', '{"message":"session aborted","detail":"aborted by verifier: Wrong approach: register the PNG decoder instead of replacing the library."}'),
  ('00000000-0000-7000-8000-000000051314', '00000000-0000-7000-8000-000000002113', '00000000-0000-7000-8000-000000313051', now() - interval '277 minutes', 'done', '{"status":"failed","reason":"aborted by verifier"}'),
  ('00000000-0000-7000-8000-000000051315', '00000000-0000-7000-8000-000000002113', NULL, now() - interval '277 minutes', 'state_change', '{"entity":"run","from":"running","to":"failed"}'),
  ('00000000-0000-7000-8000-000000051316', '00000000-0000-7000-8000-000000002113', NULL, now() - interval '277 minutes', 'state_change', '{"entity":"issue","from":"running","to":"failed"}'),
  ('00000000-0000-7000-8000-000000051501', '00000000-0000-7000-8000-000000002115', NULL, now() - interval '600 minutes', 'state_change', '{"entity":"run","from":"pending","to":"running"}'),
  ('00000000-0000-7000-8000-000000051502', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315011', now() - interval '598 minutes', 'assistant_message', '{"text":"Building the profile page."}'),
  ('00000000-0000-7000-8000-000000051503', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315011', now() - interval '590 minutes', 'done', '{"status":"succeeded","commit_sha":"aa11bb2"}'),
  ('00000000-0000-7000-8000-000000051504', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315021', now() - interval '589 minutes', 'assistant_message', '{"text":"Writing the profile journey in Bruno."}'),
  ('00000000-0000-7000-8000-000000051505', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315021', now() - interval '585 minutes', 'done', '{"status":"succeeded","commit_sha":"cc33dd4"}'),
  ('00000000-0000-7000-8000-000000051506', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315031', now() - interval '584 minutes', 'gate_result', '{"type":"preserve","verdict":"pass","details":{"step":"e2e","files":["e2e/profile.bru","e2e/profile-avatar.bru"],"changed":[]}}'),
  ('00000000-0000-7000-8000-000000051507', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315041', now() - interval '580 minutes', 'gate_result', '{"type":"agent-verdict","verdict":"pass","details":{"reviews":[{"role":"security-reviewer","verdict":"pass","summary":"No new inputs reach the database unescaped."},{"role":"style-reviewer","verdict":"pass","summary":"Matches the page conventions."},{"role":"perf-reviewer","error":"the reviewer ran out of turns"}]}}'),
  ('00000000-0000-7000-8000-000000051508', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315051', now() - interval '575 minutes', 'gate_result', '{"type":"preview","verdict":"pass","details":{"changeRequest":15,"sha":"cc33dd4e5f60718293a4b5c6d7e8f90a1b2c3d4e","url":"https://pr-15.preview.example.invalid","summary":"14 of 14 requests passed"}}'),
  ('00000000-0000-7000-8000-000000051509', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315061', now() - interval '560 minutes', 'state_change', '{"entity":"step_run","from":"running","to":"succeeded"}'),
  ('00000000-0000-7000-8000-000000051510', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315071', now() - interval '555 minutes', 'gate_result', '{"type":"provider-checks","verdict":"pass","details":{"state":"success","checks":[{"name":"lint","state":"success"},{"name":"test","state":"success","summary":"212 tests"},{"name":"codeql","state":"neutral","summary":"skipped on forks"}]}}'),
  ('00000000-0000-7000-8000-000000051511', '00000000-0000-7000-8000-000000002115', '00000000-0000-7000-8000-000000315081', now() - interval '550 minutes', 'state_change', '{"entity":"change_request","from":"open","to":"merged"}'),
  ('00000000-0000-7000-8000-000000051512', '00000000-0000-7000-8000-000000002115', NULL, now() - interval '550 minutes', 'state_change', '{"entity":"run","from":"running","to":"succeeded"}'),
  ('00000000-0000-7000-8000-000000051513', '00000000-0000-7000-8000-000000002115', NULL, now() - interval '550 minutes', 'state_change', '{"entity":"issue","from":"queued","to":"merged"}'),
  ('00000000-0000-7000-8000-000000051601', '00000000-0000-7000-8000-000000002116', '00000000-0000-7000-8000-000000316011', now() - interval '1440 minutes', 'assistant_message', '{"text":"Adding the OAuth callback."}'),
  ('00000000-0000-7000-8000-000000051602', '00000000-0000-7000-8000-000000002116', NULL, now() - interval '1430 minutes', 'state_change', '{"entity":"run","from":"running","to":"canceled"}'),
  ('00000000-0000-7000-8000-000000052301', '00000000-0000-7000-8000-000000002303', '00000000-0000-7000-8000-000000323021', now() - interval '297 minutes', 'assistant_message', '{"text":"Likely culprit: vfy-15 (Profile page) added the avatar upload that fails on PNG files; the failed tests \"profile/upload avatar png\" and \"profile/get avatar\" both go through it."}');

-- Rows inserted in one transaction share now() as created_at; order them as
-- they happened, since lists sort by it.
UPDATE step_runs SET created_at = started_at WHERE started_at IS NOT NULL;
UPDATE issues SET created_at = now() - interval '4 days' + number * interval '10 minutes', updated_at = now() - interval '1 hour';
UPDATE runs SET created_at = started_at;
UPDATE releases SET created_at = finished_at - interval '10 minutes' WHERE finished_at IS NOT NULL;

COMMIT;
