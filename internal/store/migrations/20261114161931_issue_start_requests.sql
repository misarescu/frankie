-- +goose Up
-- modify "issues" table
ALTER TABLE "issues" ADD COLUMN "start_requested_at" timestamptz NULL, ADD COLUMN "start_requested_by" text NULL, ADD COLUMN "scheduled_at" timestamptz NULL;

-- +goose Down
-- reverse: modify "issues" table
ALTER TABLE "issues" DROP COLUMN "scheduled_at", DROP COLUMN "start_requested_by", DROP COLUMN "start_requested_at";
