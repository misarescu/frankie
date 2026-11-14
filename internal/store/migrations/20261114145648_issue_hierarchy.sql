-- +goose Up
-- modify "issues" table
ALTER TABLE "issues" ADD COLUMN "parent_id" uuid NULL, ADD CONSTRAINT "issues_parent_id_fkey" FOREIGN KEY ("parent_id") REFERENCES "issues" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION;
-- create index "issues_parent_id_idx" to table: "issues"
CREATE INDEX "issues_parent_id_idx" ON "issues" ("parent_id");
-- create "issue_dependencies" table
CREATE TABLE "issue_dependencies" (
  "issue_id" uuid NOT NULL,
  "depends_on_id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("issue_id", "depends_on_id"),
  CONSTRAINT "issue_dependencies_depends_on_id_fkey" FOREIGN KEY ("depends_on_id") REFERENCES "issues" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "issue_dependencies_issue_id_fkey" FOREIGN KEY ("issue_id") REFERENCES "issues" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "issue_dependencies_not_self" CHECK (issue_id <> depends_on_id)
);
-- create index "issue_dependencies_depends_on_id_idx" to table: "issue_dependencies"
CREATE INDEX "issue_dependencies_depends_on_id_idx" ON "issue_dependencies" ("depends_on_id");

-- +goose Down
-- reverse: create index "issue_dependencies_depends_on_id_idx" to table: "issue_dependencies"
DROP INDEX "issue_dependencies_depends_on_id_idx";
-- reverse: create "issue_dependencies" table
DROP TABLE "issue_dependencies";
-- reverse: create index "issues_parent_id_idx" to table: "issues"
DROP INDEX "issues_parent_id_idx";
-- reverse: modify "issues" table
ALTER TABLE "issues" DROP CONSTRAINT "issues_parent_id_fkey", DROP COLUMN "parent_id";
