package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// MinTokenLen is the shortest API token the control plane accepts (D58).
const MinTokenLen = 32

// requireToken lets a request through only with "Authorization: Bearer <Server.Token>"
// (D58). The UI sends the header itself, so a browser never attaches it on a page's
// behalf and cross-site requests cannot use it. Without a configured token every
// request is refused: the API fails closed.
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Token == "" {
			http.Error(w, "the API has no token configured", http.StatusServiceUnavailable)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !tokenEqual(strings.TrimSpace(got), s.Token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="frankie"`)
			http.Error(w, "missing or wrong API token", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// tokenEqual compares in constant time; hashing first hides the token's length too.
func tokenEqual(got, want string) bool {
	g, w := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}
