-- +goose Up
-- modify "issues" table
ALTER TABLE "issues" ADD COLUMN "spec_proposal" jsonb NULL;

-- +goose Down
-- reverse: modify "issues" table
ALTER TABLE "issues" DROP COLUMN "spec_proposal";
