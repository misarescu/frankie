package images

import (
	"context"
	"fmt"
	"regexp"
)

// Builder builds a toolchain image and returns once it is pushed, with the
// digest the build reported. Build is idempotent per request: concurrent and
// repeated calls for the same image share one build.
type Builder interface {
	Build(ctx context.Context, r BuildRequest) (digest string, err error)
}

// Digests remembers the digest each toolchain image was built as. It is the
// only record of which image a tag means: the registry is writable by build
// pods, which run unreviewed mise.toml content, so a tag there proves nothing
// (open question 15, D45).
type Digests interface {
	Lookup(ctx context.Context, repository, tag string) (digest string, found bool, err error)
	Record(ctx context.Context, repository, tag, digest string) error
}

// Image is a resolved image, recorded by digest.
type Image struct {
	// Ref is the reference pods pull, by digest, on the node-facing registry
	// host: an overwritten tag cannot change what runs.
	Ref        string
	Repository string
	Digest     string
	// Built is true when this call had to build the image.
	Built bool
}

// Ensurer returns the image for a spec, building it only when no build of
// these inputs is on record or the registry lost it.
type Ensurer struct {
	Registry Registry
	Builder  Builder
	Digests  Digests
	// PullHost is how nodes reach the registry, e.g. localhost:30500. It can
	// differ from the host builds push to, which is cluster-internal.
	PullHost string
}

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Ensure resolves the image spec describes in the registry repository
// (Repository or DependencyRepository of a factory repo).
func (e Ensurer) Ensure(ctx context.Context, repository string, spec Spec) (Image, error) {
	if err := spec.Validate(); err != nil {
		return Image{}, fmt.Errorf("image inputs for %s: %w", repository, err)
	}
	if !repositoryRE.MatchString(repository) {
		return Image{}, fmt.Errorf("images: invalid repository %q", repository)
	}
	repo := repository
	tag := spec.Tag()
	digest, found, err := e.Digests.Lookup(ctx, repository, tag)
	if err != nil {
		return Image{}, fmt.Errorf("look up image: %w", err)
	}
	if found {
		ok, err := e.Registry.HasManifest(ctx, repository, digest)
		if err != nil {
			return Image{}, fmt.Errorf("check image: %w", err)
		}
		if ok {
			return e.image(repository, digest, false), nil
		}
		// The registry lost the image (its storage was reset): build it again.
	}

	digest, err = e.Builder.Build(ctx, BuildRequest{Repository: repository, Spec: spec})
	if err != nil {
		return Image{}, fmt.Errorf("build %s: %w", repo, err)
	}
	if !digestRE.MatchString(digest) {
		return Image{}, fmt.Errorf("build %s: the build reported %q, not an image digest", repo, digest)
	}
	// The manifest's address is its content, so finding it under the reported
	// digest proves the registry holds exactly that image.
	ok, err := e.Registry.HasManifest(ctx, repository, digest)
	switch {
	case err != nil:
		return Image{}, fmt.Errorf("check built image: %w", err)
	case !ok:
		return Image{}, fmt.Errorf("build %s: %s is not in the registry", repo, digest)
	}
	if err := e.Digests.Record(ctx, repository, tag, digest); err != nil {
		return Image{}, fmt.Errorf("record image: %w", err)
	}
	// The tag is for people browsing the registry; nothing trusts it, so a
	// failure to set it does not fail the build.
	_ = e.Registry.Tag(ctx, repository, digest, tag)
	return e.image(repository, digest, true), nil
}

func (e Ensurer) image(repository, digest string, built bool) Image {
	return Image{Ref: fmt.Sprintf("%s/%s@%s", e.PullHost, repository, digest), Repository: repository, Digest: digest, Built: built}
}
