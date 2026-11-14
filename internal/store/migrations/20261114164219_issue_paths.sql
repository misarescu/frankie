-- +goose Up
-- modify "issues" table
ALTER TABLE "issues" ADD COLUMN "file_hints" text[] NOT NULL DEFAULT '{}', ADD COLUMN "changed_paths" text[] NOT NULL DEFAULT '{}';

-- +goose Down
-- reverse: modify "issues" table
ALTER TABLE "issues" DROP COLUMN "changed_paths", DROP COLUMN "file_hints";
