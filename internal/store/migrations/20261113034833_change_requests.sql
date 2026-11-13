-- +goose Up
-- create "change_requests" table
CREATE TABLE "change_requests" (
  "id" uuid NOT NULL,
  "issue_id" uuid NOT NULL,
  "run_id" uuid NOT NULL,
  "repo" text NOT NULL,
  "number" integer NOT NULL,
  "provider_ref" text NOT NULL,
  "branch" text NOT NULL,
  "head_sha" text NOT NULL,
  "status" text NOT NULL DEFAULT 'open',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "change_requests_repo_number_key" UNIQUE ("repo", "number"),
  CONSTRAINT "change_requests_issue_id_fkey" FOREIGN KEY ("issue_id") REFERENCES "issues" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "change_requests_run_id_fkey" FOREIGN KEY ("run_id") REFERENCES "runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "change_requests_status_check" CHECK (status = ANY (ARRAY['open'::text, 'merged'::text, 'closed'::text]))
);

-- +goose Down
-- reverse: create "change_requests" table
DROP TABLE "change_requests";
