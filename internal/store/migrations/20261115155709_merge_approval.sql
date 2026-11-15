-- +goose Up
-- modify "approvals" table
ALTER TABLE "approvals" DROP CONSTRAINT "approvals_status_check", ADD CONSTRAINT "approvals_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text, 'timed_out'::text, 'changes_requested'::text])), ADD COLUMN "can_request_changes" boolean NOT NULL DEFAULT false;

-- +goose Down
-- reverse: modify "approvals" table
ALTER TABLE "approvals" DROP COLUMN "can_request_changes", DROP CONSTRAINT "approvals_status_check", ADD CONSTRAINT "approvals_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text, 'timed_out'::text]));
