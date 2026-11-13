-- +goose Up
-- create "approvals" table
CREATE TABLE "approvals" (
  "id" uuid NOT NULL,
  "step_run_id" uuid NOT NULL,
  "run_id" uuid NOT NULL,
  "reason" text NOT NULL,
  "commit_sha" text NULL,
  "change_request_url" text NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "decided_by" text NULL,
  "comment" text NULL,
  "expires_at" timestamptz NOT NULL,
  "decided_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "approvals_step_run_key" UNIQUE ("step_run_id"),
  CONSTRAINT "approvals_run_id_fkey" FOREIGN KEY ("run_id") REFERENCES "runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "approvals_step_run_id_fkey" FOREIGN KEY ("step_run_id") REFERENCES "step_runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "approvals_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'approved'::text, 'rejected'::text, 'timed_out'::text]))
);
-- create index "approvals_status_idx" to table: "approvals"
CREATE INDEX "approvals_status_idx" ON "approvals" ("status", "created_at");

-- +goose Down
-- reverse: create index "approvals_status_idx" to table: "approvals"
DROP INDEX "approvals_status_idx";
-- reverse: create "approvals" table
DROP TABLE "approvals";
