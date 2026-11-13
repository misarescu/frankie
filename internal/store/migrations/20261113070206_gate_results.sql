-- +goose Up
-- create "gate_results" table
CREATE TABLE "gate_results" (
  "id" uuid NOT NULL,
  "step_run_id" uuid NOT NULL,
  "gate_type" text NOT NULL,
  "policy" text NOT NULL,
  "verdict" text NOT NULL,
  "details" jsonb NOT NULL DEFAULT '{}',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "gate_results_step_run_key" UNIQUE ("step_run_id"),
  CONSTRAINT "gate_results_step_run_id_fkey" FOREIGN KEY ("step_run_id") REFERENCES "step_runs" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "gate_results_gate_type_check" CHECK (gate_type = ANY (ARRAY['command'::text, 'agent-verdict'::text, 'human'::text])),
  CONSTRAINT "gate_results_policy_check" CHECK (policy = ANY (ARRAY['blocking'::text, 'advisory'::text])),
  CONSTRAINT "gate_results_verdict_check" CHECK (verdict = ANY (ARRAY['pass'::text, 'fail'::text, 'escalated'::text]))
);

-- +goose Down
-- reverse: create "gate_results" table
DROP TABLE "gate_results";
