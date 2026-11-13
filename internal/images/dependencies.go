package images

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
)

// Dependencies is a dependency image (M3.1, docs/spec/05): the repo's
// toolchain image plus its dependencies, downloaded and where possible
// pre-compiled by the repo's own warm-up command from the files it declared.
// It works for any ecosystem: each tool caches where it always does under the
// agent's home, which the image layer keeps. The repo's own code is never in
// it, so it changes only when the declared files or the toolchain do.
type Dependencies struct {
	// Toolchain is the recorded toolchain image the dependencies build on.
	Toolchain Image
	// Files are the declared files by repo path.
	Files map[string][]byte
	// Run is the warm-up command, run from the directory holding the files.
	Run string
}

// Limits on what a repo can put into its dependency image's context.
const (
	maxDependencyFiles = 200
	maxDependencyBytes = 512 << 10
	// dependencyDir is where the files are copied and the warm-up runs. It is
	// under the trusted mise path and hidden at run time by the pods'
	// workspace volume, so nothing in it is mistaken for the repo.
	dependencyDir = "/workspace/deps"
)

// Validate rejects a dependency image that cannot be built.
func (d Dependencies) Validate() error {
	var errs []error
	if !repositoryRE.MatchString(d.Toolchain.Repository) || !digestRE.MatchString(d.Toolchain.Digest) {
		errs = append(errs, fmt.Errorf("toolchain image %q@%q is not a recorded image", d.Toolchain.Repository, d.Toolchain.Digest))
	}
	switch {
	case len(d.Files) == 0:
		errs = append(errs, errors.New("no dependency files"))
	case len(d.Files) > maxDependencyFiles:
		errs = append(errs, fmt.Errorf("%d dependency files, at most %d", len(d.Files), maxDependencyFiles))
	}
	total := 0
	for p, b := range d.Files {
		if !fs.ValidPath(p) || p == "." {
			errs = append(errs, fmt.Errorf("dependency file %q is not a repo path", p))
		}
		total += len(b)
	}
	if total > maxDependencyBytes {
		errs = append(errs, fmt.Errorf("dependency files are %d bytes, the limit is %d", total, maxDependencyBytes))
	}
	if strings.TrimSpace(d.Run) == "" {
		errs = append(errs, errors.New("no warm-up command"))
	}
	return errors.Join(errs...)
}

// Hash is the hex SHA-256 of everything the image depends on. Fields are
// length-prefixed so moving bytes between them always changes the hash.
func (d Dependencies) Hash() string {
	h := sha256.New()
	write := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	write([]byte(d.Toolchain.Repository + "@" + d.Toolchain.Digest))
	write([]byte(d.Run))
	for _, p := range slices.Sorted(maps.Keys(d.Files)) {
		write([]byte(p))
		write(d.Files[p])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Tag implements Spec.
func (d Dependencies) Tag() string { return "dp-" + d.Hash()[:32] }

// Context implements Spec: the declared files under deps/ and a Dockerfile
// that runs the warm-up on the toolchain image, pulled by digest.
func (d Dependencies) Context(pushHost string, args []string) (map[string][]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	run, err := json.Marshal([]string{"/bin/sh", "-c", "cd " + dependencyDir + " && " + d.Run})
	if err != nil {
		return nil, fmt.Errorf("images: warm-up command: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s/%s@%s\n", pushHost, d.Toolchain.Repository, d.Toolchain.Digest)
	for _, a := range args {
		fmt.Fprintf(&b, "ARG %s\n", a)
	}
	fmt.Fprintf(&b, "COPY --chown=%[1]d:%[1]d deps/ %[2]s/\nRUN %[3]s\n", agentUID, dependencyDir, run)
	files := map[string][]byte{dockerfileName: []byte(b.String())}
	for p, content := range d.Files {
		files["deps/"+p] = content
	}
	return files, nil
}

// DependencyRepository maps a factory repo to its registry repository for
// dependency images.
func DependencyRepository(repo string) (string, error) {
	r := "dependencies/" + strings.ToLower(repo)
	if !repositoryRE.MatchString(r) {
		return "", fmt.Errorf("images: repo %q cannot be mapped to a registry repository", repo)
	}
	return r, nil
}
