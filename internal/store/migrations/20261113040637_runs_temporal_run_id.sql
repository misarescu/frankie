-- +goose Up
-- modify "runs" table
ALTER TABLE "runs" DROP CONSTRAINT "runs_temporal_workflow_id_key", ADD COLUMN "temporal_run_id" text NULL, ADD CONSTRAINT "runs_temporal_run_id_key" UNIQUE ("temporal_run_id");

-- +goose Down
-- reverse: modify "runs" table
ALTER TABLE "runs" DROP CONSTRAINT "runs_temporal_run_id_key", DROP COLUMN "temporal_run_id", ADD CONSTRAINT "runs_temporal_workflow_id_key" UNIQUE ("temporal_workflow_id");
