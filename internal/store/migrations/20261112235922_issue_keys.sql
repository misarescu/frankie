-- +goose Up
-- modify "issues" table
ALTER TABLE "issues" ADD CONSTRAINT "issues_number_check" CHECK (number >= 1), ADD COLUMN "number" integer NOT NULL, ADD COLUMN "key" text NOT NULL, ADD CONSTRAINT "issues_key_key" UNIQUE ("key"), ADD CONSTRAINT "issues_project_number_key" UNIQUE ("project_id", "number");
-- modify "projects" table
ALTER TABLE "projects" ADD CONSTRAINT "projects_key_prefix_check" CHECK (key_prefix ~ '^[a-z][a-z0-9]{1,9}$'::text), ADD CONSTRAINT "projects_next_issue_number_check" CHECK (next_issue_number >= 1), ADD COLUMN "key_prefix" text NOT NULL, ADD COLUMN "next_issue_number" integer NOT NULL DEFAULT 1, ADD CONSTRAINT "projects_key_prefix_key" UNIQUE ("key_prefix");

-- +goose Down
-- reverse: modify "projects" table
ALTER TABLE "projects" DROP CONSTRAINT "projects_key_prefix_key", DROP COLUMN "next_issue_number", DROP COLUMN "key_prefix", DROP CONSTRAINT "projects_next_issue_number_check", DROP CONSTRAINT "projects_key_prefix_check";
-- reverse: modify "issues" table
ALTER TABLE "issues" DROP CONSTRAINT "issues_project_number_key", DROP CONSTRAINT "issues_key_key", DROP COLUMN "key", DROP COLUMN "number", DROP CONSTRAINT "issues_number_check";
