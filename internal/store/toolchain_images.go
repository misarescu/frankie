package store

import (
	"context"
	"errors"
	"time"

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

// ToolchainImage is an image on record.
type ToolchainImage struct {
	Repository, Tag, Digest string
	BuiltAt                 time.Time
}

// List returns every image on record, for registry collection (D84).
func (d ToolchainDigests) List(ctx context.Context) ([]ToolchainImage, error) {
	rows, err := d.Store.q.ListToolchainImages(ctx)
	if err != nil {
		return nil, mapErr("list toolchain images", err)
	}
	out := make([]ToolchainImage, len(rows))
	for i, r := range rows {
		out[i] = ToolchainImage{Repository: r.Repository, Tag: r.Tag, Digest: r.Digest, BuiltAt: r.BuiltAt}
	}
	return out, nil
}

// Forget removes repository:tag's record once its image is deleted; Lookup
// then finds nothing and the image is built again when needed.
func (d ToolchainDigests) Forget(ctx context.Context, repository, tag string) error {
	return mapErr("forget toolchain image", d.Store.q.DeleteToolchainImage(ctx,
		db.DeleteToolchainImageParams{Repository: repository, Tag: tag}))
}
