package images

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Registry is what the control plane does with the toolchain registry.
type Registry interface {
	// HasManifest reports whether repository holds the manifest with digest.
	HasManifest(ctx context.Context, repository, digest string) (bool, error)
	// Tag points tag at the manifest with digest.
	Tag(ctx context.Context, repository, digest, tag string) error
}

// HTTPRegistry talks to a Docker Registry HTTP API v2 server such as registry:2
// (the concrete profile's in-cluster registry, docs/spec/09).
type HTTPRegistry struct {
	BaseURL string // e.g. http://frankie-registry.frankie.svc:5000
	Client  *http.Client
}

// manifestTypes are the manifest media types a registry may store a pushed image as.
var manifestTypes = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}, ", ")

const maxManifest = 4 << 20

func (r HTTPRegistry) do(ctx context.Context, method, repository, reference, contentType string, body []byte) (*http.Response, error) {
	u, err := url.JoinPath(r.BaseURL, "v2", repository, "manifests", reference)
	if err != nil {
		return nil, fmt.Errorf("registry url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("registry request: %w", err)
	}
	req.Header.Set("Accept", manifestTypes)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c := r.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry %s %s@%s: %w", method, repository, reference, err)
	}
	return resp, nil
}

// HasManifest implements Registry with a HEAD request on the manifest by digest.
func (r HTTPRegistry) HasManifest(ctx context.Context, repository, digest string) (bool, error) {
	if !digestRE.MatchString(digest) {
		return false, fmt.Errorf("registry: %q is not a digest", digest)
	}
	resp, err := r.do(ctx, http.MethodHead, repository, digest, "", nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		if got := resp.Header.Get("Docker-Content-Digest"); got != "" && got != digest {
			return false, fmt.Errorf("registry answered %s for %s", got, digest)
		}
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("registry lookup %s@%s: unexpected status %s", repository, digest, resp.Status)
	}
}

// Tag implements Registry: it reads the manifest by digest and puts it back
// under the tag.
func (r HTTPRegistry) Tag(ctx context.Context, repository, digest, tag string) error {
	resp, err := r.do(ctx, http.MethodGet, repository, digest, "", nil)
	if err != nil {
		return err
	}
	manifest, err := io.ReadAll(io.LimitReader(resp.Body, maxManifest))
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("registry: read manifest %s: %w", digest, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry: get manifest %s@%s: %s", repository, digest, resp.Status)
	}
	put, err := r.do(ctx, http.MethodPut, repository, tag, resp.Header.Get("Content-Type"), manifest)
	if err != nil {
		return err
	}
	_ = put.Body.Close()
	if put.StatusCode != http.StatusCreated {
		return fmt.Errorf("registry: tag %s:%s: %s", repository, tag, put.Status)
	}
	return nil
}
