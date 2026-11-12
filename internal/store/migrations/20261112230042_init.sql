-- +goose Up
-- create "projects" table
CREATE TABLE "projects" (
  "id" uuid NOT NULL,
  "name" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "projects_name_key" UNIQUE ("name")
);
-- create "issues" table
CREATE TABLE "issues" (
  "id" uuid NOT NULL,
  "project_id" uuid NOT NULL,
  "type" text NOT NULL,
  "title" text NOT NULL,
  "spec" text NOT NULL DEFAULT '',
  "acceptance_criteria" text[] NOT NULL DEFAULT '{}',
  "status" text NOT NULL DEFAULT 'draft',
  "priority" integer NOT NULL DEFAULT 0,
  "source_ref" text NULL,
  "labels" text[] NOT NULL DEFAULT '{}',
  "created_by" text NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "issues_project_source_ref_key" UNIQUE ("project_id", "source_ref"),
  CONSTRAINT "issues_project_id_fkey" FOREIGN KEY ("project_id") REFERENCES "projects" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "issues_status_check" CHECK (status = ANY (ARRAY['draft'::text, 'refining'::text, 'ready'::text, 'running'::text, 'awaiting_human'::text, 'queued'::text, 'merged'::text, 'released'::text, 'failed'::text, 'canceled'::text])),
  CONSTRAINT "issues_type_check" CHECK (type = ANY (ARRAY['epic'::text, 'story'::text, 'task'::text, 'bug'::text]))
);
-- create index "issues_project_status_idx" to table: "issues"
CREATE INDEX "issues_project_status_idx" ON "issues" ("project_id", "status");
-- create "runs" table
CREATE TABLE "runs" (
  "id" uuid NOT NULL,
  "issue_id" uuid NOT NULL,
  "workflow_snapshot" jsonb NOT NULL,
  "workflow_version" text NOT NULL,
  "temporal_workflow_id" text NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "loop_counts" jsonb NOT NULL DEFAULT '{}',
  "started_at" timestamptz NULL,
  "finished_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "runs_temporal_workflow_id_key" UNIQUE ("temporal_workflow_id"),
  CONSTRAINT "runs_issue_id_fkey" FOREIGN KEY ("issue_id") REFERENCES "issues" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "runs_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'canceled'::text]))
);
-- create index "runs_one_active_per_issue_idx" to table: "runs"
CREATE UNIQUE INDEX "runs_one_active_per_issue_idx" ON "runs" ("issue_id") WHERE (status = ANY (ARRAY['pending'::text, 'running'::text]));
-- create "step_runs" table
CREATE TABLE "step_runs" (
  "id" uuid NOT NULL,
  "run_id" uuid NOT NULL,
  "step_id" text NOT NULL,
  "attempt" integer NOT NULL,
  "kind" text NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "pod_name" text NULL,
  "commit_sha" text NULL,
  "started_at" timestamptz NULL,
  "finished_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "step_runs_run_step_attempt_key" UNIQUE ("run_id", "step_id", "attempt"),
  CONSTRAINT "step_runs_run_id_fkey" FOREIGN KEY ("run_id") REFERENCES "runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "step_runs_attempt_check" CHECK (attempt >= 1),
  CONSTRAINT "step_runs_kind_check" CHECK (kind = ANY (ARRAY['agent'::text, 'gate'::text, 'action'::text, 'parallel'::text])),
  CONSTRAINT "step_runs_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'canceled'::text]))
);
-- create "events" table
CREATE TABLE "events" (
  "id" uuid NOT NULL,
  "seq" bigint NOT NULL GENERATED ALWAYS AS IDENTITY,
  "run_id" uuid NULL,
  "step_run_id" uuid NULL,
  "session_id" uuid NULL,
  "ts" timestamptz NOT NULL,
  "type" text NOT NULL,
  "payload" jsonb NOT NULL DEFAULT '{}',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "events_seq_key" UNIQUE ("seq"),
  CONSTRAINT "events_run_id_fkey" FOREIGN KEY ("run_id") REFERENCES "runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "events_step_run_id_fkey" FOREIGN KEY ("step_run_id") REFERENCES "step_runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- create index "events_run_seq_idx" to table: "events"
CREATE INDEX "events_run_seq_idx" ON "events" ("run_id", "seq");

-- +goose Down
-- reverse: create index "events_run_seq_idx" to table: "events"
DROP INDEX "events_run_seq_idx";
-- reverse: create "events" table
DROP TABLE "events";
-- reverse: create "step_runs" table
DROP TABLE "step_runs";
-- reverse: create index "runs_one_active_per_issue_idx" to table: "runs"
DROP INDEX "runs_one_active_per_issue_idx";
-- reverse: create "runs" table
DROP TABLE "runs";
-- reverse: create index "issues_project_status_idx" to table: "issues"
DROP INDEX "issues_project_status_idx";
-- reverse: create "issues" table
DROP TABLE "issues";
-- reverse: create "projects" table
DROP TABLE "projects";
