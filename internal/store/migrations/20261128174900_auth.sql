-- +goose Up
-- create "users" table
CREATE TABLE "users" (
  "id" uuid NOT NULL,
  "username" text NOT NULL,
  "display_name" text NOT NULL DEFAULT '',
  "email" text NULL,
  "role" text NOT NULL,
  "password_hash" text NOT NULL DEFAULT '',
  "disabled_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "users_username_key" UNIQUE ("username"),
  CONSTRAINT "users_role_check" CHECK (role = ANY (ARRAY['admin'::text, 'member'::text, 'viewer'::text])),
  CONSTRAINT "users_username_check" CHECK (username ~ '^[a-z0-9][a-z0-9._-]{0,63}$'::text)
);
-- create index "users_email_lower_idx" to table: "users"
CREATE UNIQUE INDEX "users_email_lower_idx" ON "users" ((lower(email))) WHERE ((email IS NOT NULL) AND (email <> ''::text));
-- create "api_tokens" table
CREATE TABLE "api_tokens" (
  "id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "name" text NOT NULL,
  "token_prefix" text NOT NULL,
  "token_hash" bytea NOT NULL,
  "last_used_at" timestamptz NULL,
  "expires_at" timestamptz NULL,
  "revoked_at" timestamptz NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "api_tokens_token_hash_key" UNIQUE ("token_hash"),
  CONSTRAINT "api_tokens_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "api_tokens_user_id_idx" to table: "api_tokens"
CREATE INDEX "api_tokens_user_id_idx" ON "api_tokens" ("user_id");
-- create "sessions" table
CREATE TABLE "sessions" (
  "id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "token_hash" bytea NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "last_seen_at" timestamptz NOT NULL DEFAULT now(),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "sessions_token_hash_key" UNIQUE ("token_hash"),
  CONSTRAINT "sessions_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- create index "sessions_expires_at_idx" to table: "sessions"
CREATE INDEX "sessions_expires_at_idx" ON "sessions" ("expires_at");
-- create index "sessions_user_id_idx" to table: "sessions"
CREATE INDEX "sessions_user_id_idx" ON "sessions" ("user_id");
-- create "user_identities" table
CREATE TABLE "user_identities" (
  "id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "provider" text NOT NULL,
  "subject" text NOT NULL,
  "email" text NOT NULL DEFAULT '',
  "login" text NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "user_identities_provider_subject_key" UNIQUE ("provider", "subject"),
  CONSTRAINT "user_identities_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "user_identities_provider_check" CHECK (provider = ANY (ARRAY['google'::text, 'gitlab'::text, 'github'::text]))
);
-- create index "user_identities_user_id_idx" to table: "user_identities"
CREATE INDEX "user_identities_user_id_idx" ON "user_identities" ("user_id");
-- create "user_invites" table
CREATE TABLE "user_invites" (
  "id" uuid NOT NULL,
  "token_hash" bytea NOT NULL,
  "email" text NOT NULL DEFAULT '',
  "role" text NOT NULL,
  "invited_by" uuid NULL,
  "expires_at" timestamptz NOT NULL,
  "accepted_at" timestamptz NULL,
  "accepted_by" uuid NULL,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("id"),
  CONSTRAINT "user_invites_token_hash_key" UNIQUE ("token_hash"),
  CONSTRAINT "user_invites_accepted_by_fkey" FOREIGN KEY ("accepted_by") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "user_invites_invited_by_fkey" FOREIGN KEY ("invited_by") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL,
  CONSTRAINT "user_invites_role_check" CHECK (role = ANY (ARRAY['admin'::text, 'member'::text, 'viewer'::text]))
);

-- +goose Down
-- reverse: create "user_invites" table
DROP TABLE "user_invites";
-- reverse: create index "user_identities_user_id_idx" to table: "user_identities"
DROP INDEX "user_identities_user_id_idx";
-- reverse: create "user_identities" table
DROP TABLE "user_identities";
-- reverse: create index "sessions_user_id_idx" to table: "sessions"
DROP INDEX "sessions_user_id_idx";
-- reverse: create index "sessions_expires_at_idx" to table: "sessions"
DROP INDEX "sessions_expires_at_idx";
-- reverse: create "sessions" table
DROP TABLE "sessions";
-- reverse: create index "api_tokens_user_id_idx" to table: "api_tokens"
DROP INDEX "api_tokens_user_id_idx";
-- reverse: create "api_tokens" table
DROP TABLE "api_tokens";
-- reverse: create index "users_email_lower_idx" to table: "users"
DROP INDEX "users_email_lower_idx";
-- reverse: create "users" table
DROP TABLE "users";
