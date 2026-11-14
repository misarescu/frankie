-- +goose Up
-- modify "issues" table
ALTER TABLE "issues" ADD COLUMN "source_closed_at" timestamptz NULL;

-- +goose Down
-- reverse: modify "issues" table
ALTER TABLE "issues" DROP COLUMN "source_closed_at";
