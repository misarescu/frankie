package store

import (
	"context"
	"errors"

	"github.com/misarescu/frankie/internal/store/db"
)

// ToolchainDigests remembers the digest each toolchain image was built as
// (images.Digests, D45).
type ToolchainDigests struct{ Store *Store }

// Lookup returns the recorded digest of repository:tag.
func (d ToolchainDigests) Lookup(ctx context.Context, repository, tag string) (string, bool, error) {
	digest, err := d.Store.q.GetToolchainImageDigest(ctx, db.GetToolchainImageDigestParams{Repository: repository, Tag: tag})
	if err != nil {
		err = mapErr("get toolchain image digest", err)
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return digest, true, nil
}

// Record stores the digest repository:tag was built as, replacing an earlier one.
func (d ToolchainDigests) Record(ctx context.Context, repository, tag, digest string) error {
	return mapErr("record toolchain image digest", d.Store.q.RecordToolchainImageDigest(ctx,
		db.RecordToolchainImageDigestParams{Repository: repository, Tag: tag, Digest: digest}))
}
