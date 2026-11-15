package images

import (
	"bytes"
	"context"
	"encoding/json"
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
	resp, err := r.client().Do(req)
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

// Catalog is what registry collection (D84) does with the toolchain registry.
type Catalog interface {
	// Repositories lists every repository in the registry.
	Repositories(ctx context.Context) ([]string, error)
	// Tags lists repository's tags; an unknown repository has none.
	Tags(ctx context.Context, repository string) ([]string, error)
	// Resolve returns the digest of the manifest tag points to.
	Resolve(ctx context.Context, repository, tag string) (digest string, found bool, err error)
	// Delete removes the manifest with digest and every tag pointing to it.
	// Deleting a manifest that is not there succeeds.
	Delete(ctx context.Context, repository, digest string) error
}

// maxList bounds one page of a catalog or tag listing.
const maxList = 16 << 20

// list follows a paginated listing (Docker Registry HTTP API v2: n and last,
// with a Link header naming the next page) and collects each page's field.
func (r HTTPRegistry) list(ctx context.Context, path string, page func([]byte) ([]string, error)) ([]string, error) {
	base, err := url.Parse(r.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("registry url: %w", err)
	}
	next, err := base.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("registry url: %w", err)
	}
	var all []string
	for pages := 0; next != nil; pages++ {
		if pages == 1000 {
			return nil, fmt.Errorf("registry %s: more than 1000 pages", path)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("registry request: %w", err)
		}
		resp, err := r.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("registry GET %s: %w", next.Path, err)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxList))
		_ = resp.Body.Close()
		switch {
		case err != nil:
			return nil, fmt.Errorf("registry GET %s: %w", next.Path, err)
		case resp.StatusCode == http.StatusNotFound:
			return nil, nil
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("registry GET %s: %s", next.Path, resp.Status)
		}
		items, err := page(body)
		if err != nil {
			return nil, fmt.Errorf("registry GET %s: %w", next.Path, err)
		}
		all = append(all, items...)
		next = nil
		if link := nextLink(resp.Header.Get("Link")); link != "" {
			if next, err = base.Parse(link); err != nil {
				return nil, fmt.Errorf("registry GET %s: next page %q: %w", path, link, err)
			}
		}
	}
	return all, nil
}

// nextLink returns the target of a Link header's rel="next" entry.
func nextLink(header string) string {
	for entry := range strings.SplitSeq(header, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(entry), ";")
		if ok && strings.Contains(strings.ReplaceAll(params, " ", ""), `rel="next"`) {
			return strings.Trim(strings.TrimSpace(target), "<>")
		}
	}
	return ""
}

func (r HTTPRegistry) client() *http.Client {
	if r.Client == nil {
		return http.DefaultClient
	}
	return r.Client
}

// Repositories implements Catalog.
func (r HTTPRegistry) Repositories(ctx context.Context) ([]string, error) {
	repos, err := r.list(ctx, "/v2/_catalog?n=1000", func(b []byte) ([]string, error) {
		var page struct {
			Repositories []string `json:"repositories"`
		}
		err := json.Unmarshal(b, &page)
		return page.Repositories, err
	})
	return repos, err
}

// Tags implements Catalog.
func (r HTTPRegistry) Tags(ctx context.Context, repository string) ([]string, error) {
	if !repositoryRE.MatchString(repository) {
		return nil, fmt.Errorf("registry: invalid repository %q", repository)
	}
	tags, err := r.list(ctx, "/v2/"+repository+"/tags/list?n=1000", func(b []byte) ([]string, error) {
		var page struct {
			Tags []string `json:"tags"`
		}
		err := json.Unmarshal(b, &page)
		return page.Tags, err
	})
	return tags, err
}

// Resolve implements Catalog with a HEAD request on the manifest by tag.
func (r HTTPRegistry) Resolve(ctx context.Context, repository, tag string) (string, bool, error) {
	resp, err := r.do(ctx, http.MethodHead, repository, tag, "", nil)
	if err != nil {
		return "", false, err
	}
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		digest := resp.Header.Get("Docker-Content-Digest")
		if !digestRE.MatchString(digest) {
			return "", false, fmt.Errorf("registry: %s:%s answered digest %q", repository, tag, digest)
		}
		return digest, true, nil
	case http.StatusNotFound:
		return "", false, nil
	default:
		return "", false, fmt.Errorf("registry lookup %s:%s: unexpected status %s", repository, tag, resp.Status)
	}
}

// Delete implements Catalog. The registry must allow deletes
// (REGISTRY_STORAGE_DELETE_ENABLED); its blobs stay until garbage collection.
func (r HTTPRegistry) Delete(ctx context.Context, repository, digest string) error {
	if !digestRE.MatchString(digest) {
		return fmt.Errorf("registry: %q is not a digest", digest)
	}
	resp, err := r.do(ctx, http.MethodDelete, repository, digest, "", nil)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusAccepted, http.StatusOK, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("registry: delete %s@%s: %s", repository, digest, resp.Status)
	}
}
