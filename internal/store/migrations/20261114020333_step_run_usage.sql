-- +goose Up
-- modify "step_runs" table
ALTER TABLE "step_runs" ADD CONSTRAINT "step_runs_cost_usd_check" CHECK (cost_usd >= (0)::double precision), ADD CONSTRAINT "step_runs_input_tokens_check" CHECK (input_tokens >= 0), ADD CONSTRAINT "step_runs_output_tokens_check" CHECK (output_tokens >= 0), ADD COLUMN "cost_usd" double precision NOT NULL DEFAULT 0, ADD COLUMN "input_tokens" bigint NOT NULL DEFAULT 0, ADD COLUMN "output_tokens" bigint NOT NULL DEFAULT 0;

-- +goose Down
-- reverse: modify "step_runs" table
ALTER TABLE "step_runs" DROP COLUMN "output_tokens", DROP COLUMN "input_tokens", DROP COLUMN "cost_usd", DROP CONSTRAINT "step_runs_output_tokens_check", DROP CONSTRAINT "step_runs_input_tokens_check", DROP CONSTRAINT "step_runs_cost_usd_check";
