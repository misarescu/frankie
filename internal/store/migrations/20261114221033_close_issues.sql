-- +goose Up
-- modify "issues" table
ALTER TABLE "issues" ADD COLUMN "closed_at" timestamptz NULL, ADD COLUMN "closed_by" text NULL, ADD COLUMN "close_reason" text NOT NULL DEFAULT '';

-- +goose Down
-- reverse: modify "issues" table
ALTER TABLE "issues" DROP COLUMN "close_reason", DROP COLUMN "closed_by", DROP COLUMN "closed_at";
