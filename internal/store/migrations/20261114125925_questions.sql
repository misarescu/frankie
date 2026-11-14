-- +goose Up
-- create "questions" table
CREATE TABLE "questions" (
  "id" uuid NOT NULL,
  "step_run_id" uuid NOT NULL,
  "run_id" uuid NOT NULL,
  "question_id" text NOT NULL,
  "question" text NOT NULL,
  "status" text NOT NULL DEFAULT 'pending',
  "answer" text NULL,
  "answered_by" text NULL,
  "answered_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "questions_step_run_question_key" UNIQUE ("step_run_id", "question_id"),
  CONSTRAINT "questions_run_id_fkey" FOREIGN KEY ("run_id") REFERENCES "runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "questions_step_run_id_fkey" FOREIGN KEY ("step_run_id") REFERENCES "step_runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "questions_status_check" CHECK (status = ANY (ARRAY['pending'::text, 'answered'::text, 'expired'::text]))
);
-- create index "questions_status_idx" to table: "questions"
CREATE INDEX "questions_status_idx" ON "questions" ("status", "created_at");

-- +goose Down
-- reverse: create index "questions_status_idx" to table: "questions"
DROP INDEX "questions_status_idx";
-- reverse: create "questions" table
DROP TABLE "questions";
