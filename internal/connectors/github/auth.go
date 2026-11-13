// Package github implements the GitHub connectors (GitProvider, IssueSource).
// GitHub specifics stay here; the domain never sees them (invariant 7).
package github

import "net/http"

// TokenAuthorizer authenticates git-over-HTTPS and API-adjacent requests to
// GitHub with a token (a fine-grained PAT for M1, D26). GitHub accepts any user
// name with the token as password; x-access-token is the conventional one.
func TokenAuthorizer(token string) func(*http.Request) {
	return func(r *http.Request) {
		r.SetBasicAuth("x-access-token", token)
	}
}
