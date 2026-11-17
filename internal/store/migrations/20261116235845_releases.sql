-- +goose Up
-- modify "change_requests" table
ALTER TABLE "change_requests" ADD COLUMN "merge_sha" text NULL, ADD COLUMN "merged_at" timestamptz NULL;
-- create "releases" table
CREATE TABLE "releases" (
  "id" uuid NOT NULL,
  "project_id" uuid NOT NULL,
  "repo" text NOT NULL,
  "sha" text NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "finished_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "releases_project_repo_sha_key" UNIQUE ("project_id", "repo", "sha"),
  CONSTRAINT "releases_project_id_fkey" FOREIGN KEY ("project_id") REFERENCES "projects" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "releases_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'in_progress'::text, 'succeeded'::text, 'failed'::text, 'rolled_back'::text]))
);
-- create index "releases_project_created_idx" to table: "releases"
CREATE INDEX "releases_project_created_idx" ON "releases" ("project_id", "created_at");
-- create "deployments" table
CREATE TABLE "deployments" (
  "id" uuid NOT NULL,
  "release_id" uuid NOT NULL,
  "stage" text NOT NULL,
  "driver" text NOT NULL,
  "driver_state" jsonb NOT NULL DEFAULT '{}',
  "status" text NOT NULL DEFAULT 'pending',
  "reason" text NOT NULL DEFAULT '',
  "started_at" timestamptz NULL,
  "finished_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "deployments_release_stage_key" UNIQUE ("release_id", "stage"),
  CONSTRAINT "deployments_release_id_fkey" FOREIGN KEY ("release_id") REFERENCES "releases" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "deployments_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'in_progress'::text, 'succeeded'::text, 'failed'::text, 'rolled_back'::text]))
);
-- create "release_issues" table
CREATE TABLE "release_issues" (
  "release_id" uuid NOT NULL,
  "issue_id" uuid NOT NULL,
  "position" integer NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("release_id", "issue_id"),
  CONSTRAINT "release_issues_issue_id_fkey" FOREIGN KEY ("issue_id") REFERENCES "issues" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "release_issues_release_id_fkey" FOREIGN KEY ("release_id") REFERENCES "releases" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "release_issues_position_check" CHECK ("position" >= 1)
);
-- create index "release_issues_issue_id_idx" to table: "release_issues"
CREATE INDEX "release_issues_issue_id_idx" ON "release_issues" ("issue_id");

-- +goose Down
-- reverse: create index "release_issues_issue_id_idx" to table: "release_issues"
DROP INDEX "release_issues_issue_id_idx";
-- reverse: create "release_issues" table
DROP TABLE "release_issues";
-- reverse: create "deployments" table
DROP TABLE "deployments";
-- reverse: create index "releases_project_created_idx" to table: "releases"
DROP INDEX "releases_project_created_idx";
-- reverse: create "releases" table
DROP TABLE "releases";
-- reverse: modify "change_requests" table
ALTER TABLE "change_requests" DROP COLUMN "merged_at", DROP COLUMN "merge_sha";
