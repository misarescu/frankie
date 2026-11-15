package controlplane

import (
	"context"

	"github.com/misarescu/frankie/internal/images"
	"github.com/misarescu/frankie/internal/store"
)

// toolchainImages is the images on record; store.ToolchainDigests in production.
type toolchainImages interface {
	List(ctx context.Context) ([]store.ToolchainImage, error)
	Forget(ctx context.Context, repository, tag string) error
}

// toolchainRecords is the images on record as registry collection reads them (D84).
type toolchainRecords struct{ src toolchainImages }

func (r toolchainRecords) List(ctx context.Context) ([]images.Record, error) {
	rows, err := r.src.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]images.Record, len(rows))
	for i, row := range rows {
		out[i] = images.Record{Repository: row.Repository, Tag: row.Tag, Digest: row.Digest, BuiltAt: row.BuiltAt}
	}
	return out, nil
}

func (r toolchainRecords) Forget(ctx context.Context, repository, tag string) error {
	return r.src.Forget(ctx, repository, tag)
}
