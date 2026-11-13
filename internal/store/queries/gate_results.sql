-- Idempotent per gate StepRun: a retried Activity records the same outcome again.
-- name: RecordGateResult :one
INSERT INTO gate_results (id, step_run_id, gate_type, policy, verdict, details)
VALUES (@id, @step_run_id, @gate_type, @policy, @verdict, @details)
ON CONFLICT (step_run_id) DO UPDATE
SET verdict = EXCLUDED.verdict, details = EXCLUDED.details
RETURNING *;

-- name: ListGateResultsForRun :many
SELECT g.* FROM gate_results g JOIN step_runs s ON s.id = g.step_run_id
WHERE s.run_id = @run_id
ORDER BY g.created_at, g.id;
