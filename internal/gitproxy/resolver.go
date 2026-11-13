package gitproxy

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"slices"
)

// StaticResolver serves a fixed set of repos, given as factory repo name to
// upstream URL, and authorizes every upstream request with authorize (which may
// be nil for public repos). Project config sync replaces it later.
func StaticResolver(upstreams map[string]string, authorize func(*http.Request)) (Resolver, error) {
	parsed := make(map[string]*url.URL, len(upstreams))
	for _, repo := range slices.Sorted(maps.Keys(upstreams)) {
		if !repoNameRE.MatchString(repo) {
			return nil, fmt.Errorf("gitproxy: invalid repo name %q", repo)
		}
		u, err := url.Parse(upstreams[repo])
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return nil, fmt.Errorf("gitproxy: repo %q: upstream must be an http(s) URL without credentials", repo)
		}
		parsed[repo] = u
	}
	return ResolverFunc(func(_ context.Context, repo string) (Upstream, error) {
		u, ok := parsed[repo]
		if !ok {
			return Upstream{}, fmt.Errorf("gitproxy: repo %q: %w", repo, os.ErrNotExist)
		}
		return Upstream{URL: u, Authorize: authorize}, nil
	}), nil
}
