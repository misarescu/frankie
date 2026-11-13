package github

import (
	"net/http"
	"testing"
)

func TestTokenAuthorizer(t *testing.T) {
	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://github.com/a/b.git/info/refs", nil)
	TokenAuthorizer("github_pat_123")(r)
	user, pass, ok := r.BasicAuth()
	if !ok || user != "x-access-token" || pass != "github_pat_123" {
		t.Errorf("basic auth = %q, %q, %v", user, pass, ok)
	}
}
