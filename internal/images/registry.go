package images

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Registry answers whether an image is already built.
type Registry interface {
	// Has reports whether repository:tag exists.
	Has(ctx context.Context, repository, tag string) (bool, error)
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

// Has implements Registry with a HEAD request on the manifest.
func (r HTTPRegistry) Has(ctx context.Context, repository, tag string) (bool, error) {
	u, err := url.JoinPath(r.BaseURL, "v2", repository, "manifests", tag)
	if err != nil {
		return false, fmt.Errorf("registry url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return false, fmt.Errorf("registry request: %w", err)
	}
	req.Header.Set("Accept", manifestTypes)
	c := r.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return false, fmt.Errorf("registry lookup %s:%s: %w", repository, tag, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("registry lookup %s:%s: unexpected status %s", repository, tag, resp.Status)
	}
}
