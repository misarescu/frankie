-- +goose Up
-- modify "deployments" table
ALTER TABLE "deployments" ADD COLUMN "verification" jsonb NULL;

-- +goose Down
-- reverse: modify "deployments" table
ALTER TABLE "deployments" DROP COLUMN "verification";
