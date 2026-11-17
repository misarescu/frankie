-- +goose Up
-- modify "releases" table
ALTER TABLE "releases" ADD COLUMN "bug_issue_ref" text NULL;

-- +goose Down
-- reverse: modify "releases" table
ALTER TABLE "releases" DROP COLUMN "bug_issue_ref";
