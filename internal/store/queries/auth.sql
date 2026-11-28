-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: LockActiveAdmins :many
SELECT id FROM users WHERE role = 'admin' AND disabled_at IS NULL FOR UPDATE;

-- name: CreateUser :one
INSERT INTO users (id, username, display_name, email, role, password_hash)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower($1) AND email IS NOT NULL AND email <> '';

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at ASC, id ASC;

-- name: UpdateUserRole :one
UPDATE users
SET role = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserDisabled :one
UPDATE users
SET disabled_at = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserPassword :execrows
UPDATE users
SET password_hash = $2, updated_at = now()
WHERE id = $1;

-- name: UpdateUserProfile :one
UPDATE users
SET display_name = $2, email = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateUserIdentity :one
INSERT INTO user_identities (id, user_id, provider, subject, email, login)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserIdentityBySubject :one
SELECT * FROM user_identities WHERE provider = $1 AND subject = $2;

-- name: ListUserIdentities :many
SELECT * FROM user_identities WHERE user_id = $1 ORDER BY created_at ASC, id ASC;

-- name: ListAllUserIdentities :many
SELECT * FROM user_identities ORDER BY created_at ASC, id ASC;

-- name: CountUserIdentities :one
SELECT count(*) FROM user_identities WHERE user_id = $1;

-- name: DeleteUserIdentity :execrows
DELETE FROM user_identities WHERE id = $1 AND user_id = $2;

-- name: CreateSession :one
INSERT INTO sessions (id, user_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetSessionByHash :one
SELECT
  s.id AS session_id,
  s.user_id AS session_user_id,
  s.expires_at AS session_expires_at,
  s.last_seen_at AS session_last_seen_at,
  s.created_at AS session_created_at,
  u.id,
  u.username,
  u.display_name,
  u.email,
  u.role,
  u.password_hash,
  u.disabled_at,
  u.created_at,
  u.updated_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.expires_at > now()
  AND u.disabled_at IS NULL;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = $1;

-- name: DeleteSessionByHash :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteOtherUserSessions :exec
DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2;

-- name: PruneExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= now();

-- name: CreateAPIToken :one
INSERT INTO api_tokens (id, user_id, name, token_prefix, token_hash, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetAPITokenByHash :one
SELECT
  t.id AS token_id,
  t.user_id AS token_user_id,
  t.name AS token_name,
  t.token_prefix AS token_prefix,
  t.last_used_at AS token_last_used_at,
  t.expires_at AS token_expires_at,
  t.revoked_at AS token_revoked_at,
  t.created_at AS token_created_at,
  u.id,
  u.username,
  u.display_name,
  u.email,
  u.role,
  u.password_hash,
  u.disabled_at,
  u.created_at,
  u.updated_at
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1
  AND t.revoked_at IS NULL
  AND (t.expires_at IS NULL OR t.expires_at > now())
  AND u.disabled_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = now() WHERE id = $1;

-- name: ListAPITokens :many
SELECT * FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC, id DESC;

-- name: RevokeAPIToken :execrows
UPDATE api_tokens
SET revoked_at = now()
WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL;

-- name: CreateInvite :one
INSERT INTO user_invites (id, token_hash, email, role, invited_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetInviteByHash :one
SELECT
  i.*,
  COALESCE(NULLIF(u.display_name, ''), u.username, '') AS invited_by_name
FROM user_invites i
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.token_hash = $1;

-- name: GetInviteByHashForUpdate :one
SELECT * FROM user_invites WHERE token_hash = $1 FOR UPDATE;

-- name: ListInvites :many
SELECT
  i.*,
  COALESCE(NULLIF(u.display_name, ''), u.username, '') AS invited_by_name
FROM user_invites i
LEFT JOIN users u ON u.id = i.invited_by
ORDER BY i.created_at DESC, i.id DESC;

-- name: AcceptInviteRow :one
UPDATE user_invites
SET accepted_at = now(), accepted_by = $2
WHERE id = $1 AND accepted_at IS NULL
RETURNING *;

-- name: DeleteInvite :execrows
DELETE FROM user_invites WHERE id = $1;
