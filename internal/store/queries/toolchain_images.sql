-- name: GetToolchainImageDigest :one
SELECT digest FROM toolchain_images WHERE repository = @repository AND tag = @tag;

-- A rebuild (the registry lost the image) replaces the digest.
-- name: RecordToolchainImageDigest :exec
INSERT INTO toolchain_images (repository, tag, digest) VALUES (@repository, @tag, @digest)
ON CONFLICT (repository, tag) DO UPDATE SET digest = EXCLUDED.digest, built_at = now();

-- Registry collection (D84) lists every image on record.
-- name: ListToolchainImages :many
SELECT repository, tag, digest, built_at FROM toolchain_images ORDER BY repository, built_at DESC, tag;

-- name: DeleteToolchainImage :exec
DELETE FROM toolchain_images WHERE repository = @repository AND tag = @tag;
