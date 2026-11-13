-- +goose Up
-- create "toolchain_images" table
CREATE TABLE "toolchain_images" (
  "repository" text NOT NULL,
  "tag" text NOT NULL,
  "digest" text NOT NULL,
  "built_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("repository", "tag"),
  CONSTRAINT "toolchain_images_digest_check" CHECK (digest ~ '^sha256:[0-9a-f]{64}$'::text)
);

-- +goose Down
-- reverse: create "toolchain_images" table
DROP TABLE "toolchain_images";
