-- name: AppendEvent :one
INSERT INTO events (id, run_id, step_run_id, session_id, ts, type, payload)
VALUES (@id, sqlc.narg('run_id'), sqlc.narg('step_run_id'), sqlc.narg('session_id'), @ts, @type, @payload)
RETURNING *;

-- Replay or resume a run's stream: events strictly after after_seq, in order.
-- name: ListRunEvents :many
SELECT * FROM events
WHERE run_id = @run_id AND seq > @after_seq
ORDER BY seq
LIMIT @max_events;
