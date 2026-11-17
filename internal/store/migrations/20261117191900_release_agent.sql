-- +goose Up
-- modify "releases" table
ALTER TABLE "releases" ADD CONSTRAINT "releases_bump_check" CHECK (bump = ANY (ARRAY[''::text, 'major'::text, 'minor'::text, 'patch'::text])), ADD CONSTRAINT "releases_notes_source_check" CHECK (notes_source = ANY (ARRAY[''::text, 'agent'::text, 'fallback'::text])), ADD COLUMN "bump" text NOT NULL DEFAULT '', ADD COLUMN "notes" text NOT NULL DEFAULT '', ADD COLUMN "notes_source" text NOT NULL DEFAULT '', ADD COLUMN "version" text NOT NULL DEFAULT '', ADD COLUMN "provider_release_url" text NOT NULL DEFAULT '', ADD COLUMN "publish_error" text NOT NULL DEFAULT '';
-- modify "runs" table
ALTER TABLE "runs" ADD CONSTRAINT "runs_issue_or_release_check" CHECK ((issue_id IS NULL) <> (release_id IS NULL)), ALTER COLUMN "issue_id" DROP NOT NULL, ADD COLUMN "release_id" uuid NULL, ADD CONSTRAINT "runs_release_id_fkey" FOREIGN KEY ("release_id") REFERENCES "releases" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION;
-- create index "runs_release_id_idx" to table: "runs"
CREATE INDEX "runs_release_id_idx" ON "runs" ("release_id");

-- +goose Down
-- reverse: create index "runs_release_id_idx" to table: "runs"
DROP INDEX "runs_release_id_idx";
-- reverse: modify "runs" table
ALTER TABLE "runs" DROP CONSTRAINT "runs_release_id_fkey", DROP COLUMN "release_id", ALTER COLUMN "issue_id" SET NOT NULL, DROP CONSTRAINT "runs_issue_or_release_check";
-- reverse: modify "releases" table
ALTER TABLE "releases" DROP COLUMN "publish_error", DROP COLUMN "provider_release_url", DROP COLUMN "version", DROP COLUMN "notes_source", DROP COLUMN "notes", DROP COLUMN "bump", DROP CONSTRAINT "releases_notes_source_check", DROP CONSTRAINT "releases_bump_check";
