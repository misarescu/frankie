-- +goose Up
-- Hand-written data migration (D111), no schema change: issues merged before
-- releases existed are already deployed (by hand), so they are released now
-- and the first release contains only what merged after it.
UPDATE issues SET status = 'released', updated_at = now() WHERE status = 'merged';

-- +goose Down
-- Not reversed: which issues were merged before is not recorded.
