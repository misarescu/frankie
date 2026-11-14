-- Idempotent per runner question: a retried session Activity that sees the
-- question event again gets the row it wrote.
-- name: OpenQuestion :one
INSERT INTO questions (id, step_run_id, run_id, question_id, question)
VALUES (@id, @step_run_id, @run_id, @question_id, @question)
ON CONFLICT (step_run_id, question_id) DO UPDATE SET updated_at = questions.updated_at
RETURNING *;

-- name: GetQuestion :one
SELECT * FROM questions WHERE id = @id;

-- name: GetQuestionByRunnerID :one
SELECT * FROM questions WHERE step_run_id = @step_run_id AND question_id = @question_id;

-- Compare-and-set from pending, so the first answer wins.
-- name: AnswerQuestion :one
UPDATE questions
SET status = 'answered', answer = @answer, answered_by = sqlc.narg('answered_by'), answered_at = now(), updated_at = now()
WHERE step_run_id = @step_run_id AND question_id = @question_id AND status = 'pending'
RETURNING *;

-- name: ExpireQuestion :execrows
UPDATE questions SET status = 'expired', updated_at = now()
WHERE step_run_id = @step_run_id AND question_id = @question_id AND status = 'pending';

-- name: ExpireStepRunQuestions :execrows
UPDATE questions SET status = 'expired', updated_at = now()
WHERE step_run_id = @step_run_id AND status = 'pending';

-- The questions inbox: pending questions of agent steps still running, with
-- what a person needs to answer and what the API needs to signal the run's
-- workflow.
-- name: ListPendingQuestions :many
SELECT q.*, s.step_id, i.key AS issue_key, i.title AS issue_title,
       r.temporal_workflow_id, r.temporal_run_id
FROM questions q
JOIN step_runs s ON s.id = q.step_run_id
JOIN runs r ON r.id = q.run_id
JOIN issues i ON i.id = r.issue_id
WHERE q.status = 'pending' AND s.status = 'running'
ORDER BY q.created_at, q.id;

-- name: GetPendingQuestion :one
SELECT q.*, s.step_id, i.key AS issue_key, i.title AS issue_title,
       r.temporal_workflow_id, r.temporal_run_id
FROM questions q
JOIN step_runs s ON s.id = q.step_run_id
JOIN runs r ON r.id = q.run_id
JOIN issues i ON i.id = r.issue_id
WHERE q.id = @id AND q.status = 'pending' AND s.status = 'running';
