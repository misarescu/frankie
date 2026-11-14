-- +goose Up
-- modify "issues" table
ALTER TABLE "issues" ADD COLUMN "spec_approved_at" timestamptz NULL, ADD COLUMN "spec_approved_by" text NULL, ADD COLUMN "spec_rejected_at" timestamptz NULL, ADD COLUMN "spec_feedback" text NOT NULL DEFAULT '';
-- modify "projects" table
ALTER TABLE "projects" ADD COLUMN "spec_approval" boolean NOT NULL DEFAULT false;

-- +goose Down
-- reverse: modify "projects" table
ALTER TABLE "projects" DROP COLUMN "spec_approval";
-- reverse: modify "issues" table
ALTER TABLE "issues" DROP COLUMN "spec_feedback", DROP COLUMN "spec_rejected_at", DROP COLUMN "spec_approved_by", DROP COLUMN "spec_approved_at";
