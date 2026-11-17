-- +goose Up
-- modify "releases" table
ALTER TABLE "releases" ADD COLUMN "reason" text NOT NULL DEFAULT '';

-- +goose Down
-- reverse: modify "releases" table
ALTER TABLE "releases" DROP COLUMN "reason";
