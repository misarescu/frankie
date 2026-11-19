// Package buildinfo carries what a build stamps into a binary: the commit it
// was built from, which the control plane reports at GET /api/version so a
// preview's driver can tell when the preview runs its change request's head
// (D131). The control plane's image stamps it with
// -ldflags "-X github.com/misarescu/frankie/internal/buildinfo.commit=<sha>".
package buildinfo

// commit is set by the linker; it is empty in builds that do not stamp it.
var commit string

// Commit returns the commit the binary was built from, or "" when the build did
// not stamp one (go test, go run, a local build).
func Commit() string { return commit }
