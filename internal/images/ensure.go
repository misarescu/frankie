package images

import (
	"context"
	"fmt"
)

// Builder builds a toolchain image and returns once it is pushed. Build is
// idempotent per request: concurrent and repeated calls for the same image
// share one build.
type Builder interface {
	Build(ctx context.Context, r BuildRequest) error
}

// Image is a resolved toolchain image.
type Image struct {
	// Ref is the reference pods pull, on the node-facing registry host.
	Ref string
	// Built is true when this call had to build the image.
	Built bool
}

// Ensurer returns the toolchain image for a repo's inputs, building it only when
// the registry does not have it.
type Ensurer struct {
	Registry Registry
	Builder  Builder
	// PullHost is how nodes reach the registry, e.g. localhost:30500. It can
	// differ from the host builds push to, which is cluster-internal.
	PullHost string
}

// Ensure resolves the image for repo (a factory repo such as "owner/name").
func (e Ensurer) Ensure(ctx context.Context, repo string, in Inputs) (Image, error) {
	if err := in.Validate(); err != nil {
		return Image{}, fmt.Errorf("toolchain inputs for %s: %w", repo, err)
	}
	repository, err := Repository(repo)
	if err != nil {
		return Image{}, err
	}
	img := Image{Ref: fmt.Sprintf("%s/%s:%s", e.PullHost, repository, in.Tag())}
	ok, err := e.Registry.Has(ctx, repository, in.Tag())
	if err != nil {
		return Image{}, fmt.Errorf("check toolchain image: %w", err)
	}
	if ok {
		return img, nil
	}
	if err := e.Builder.Build(ctx, BuildRequest{Repository: repository, Inputs: in}); err != nil {
		return Image{}, fmt.Errorf("build toolchain image for %s: %w", repo, err)
	}
	img.Built = true
	return img, nil
}
