-- name: GetToolchainImageDigest :one
SELECT digest FROM toolchain_images WHERE repository = @repository AND tag = @tag;

-- A rebuild (the registry lost the image) replaces the digest.
-- name: RecordToolchainImageDigest :exec
INSERT INTO toolchain_images (repository, tag, digest) VALUES (@repository, @tag, @digest)
ON CONFLICT (repository, tag) DO UPDATE SET digest = EXCLUDED.digest, built_at = now();
