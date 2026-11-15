// Package images decides which image an agent step or gate runs in, and builds
// it when there is none on record yet (D15, D30, D45,
// docs/spec/05-sandbox-and-toolchains.md). A toolchain image is a pure
// function of the platform base image and the repo's mise.toml (without its
// tasks, D75) and mise.lock; a
// dependency image (M3.1) of a toolchain image and the repo's declared
// dependency files. Unchanged inputs reuse the image instead of rebuilding it.
package images

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Inputs are everything a toolchain image depends on.
type Inputs struct {
	// Base is the full reference of the platform base image the toolchain is
	// layered on, e.g. ghcr.io/misarescu/frankie-runner:sha-5459c2c.
	Base     string
	MiseToml []byte
	MiseLock []byte // optional
}

// maxFileSize keeps the build context well under the 1 MiB ConfigMap limit.
const maxFileSize = 256 << 10

// Validate rejects inputs that cannot produce a build.
func (in Inputs) Validate() error {
	var errs []error
	if in.Base == "" {
		errs = append(errs, errors.New("base image is required"))
	}
	if len(in.MiseToml) == 0 {
		errs = append(errs, errors.New("mise.toml is required"))
	}
	for name, b := range map[string][]byte{"mise.toml": in.MiseToml, "mise.lock": in.MiseLock} {
		if len(b) > maxFileSize {
			errs = append(errs, fmt.Errorf("%s is %d bytes, the limit is %d", name, len(b), maxFileSize))
		}
	}
	return errors.Join(errs...)
}

// taskTables are the mise.toml tables that define tasks. Installing tools
// never reads them, and agents and gates run tasks from the workspace's own
// mise.toml, so they are left out of the toolchain image and its hash (D75).
var taskTables = []string{"tasks", "task_config"}

// ToolsToml is mise.toml as the toolchain image uses it: without its task
// tables, so editing a task does not rebuild the toolchain. A file without task
// tables is used byte for byte, and one that does not parse as TOML too, so
// mise install reports the error as it always did.
func (in Inputs) ToolsToml() []byte {
	var doc map[string]any
	if err := toml.Unmarshal(in.MiseToml, &doc); err != nil {
		return in.MiseToml
	}
	found := false
	for _, k := range taskTables {
		if _, ok := doc[k]; ok {
			delete(doc, k)
			found = true
		}
	}
	if !found {
		return in.MiseToml
	}
	out, err := toml.Marshal(doc)
	if err != nil {
		return in.MiseToml
	}
	return out
}

// Hash is the hex SHA-256 of the inputs, with mise.toml as ToolsToml gives it.
// Fields are length-prefixed so moving bytes between them always changes the hash.
func (in Inputs) Hash() string {
	h := sha256.New()
	for _, f := range [][]byte{[]byte(in.Base), in.ToolsToml(), in.MiseLock} {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(f)))
		h.Write(n[:])
		h.Write(f)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Tag is the registry tag of the toolchain image for these inputs.
func (in Inputs) Tag() string {
	return "tc-" + in.Hash()[:32]
}

// Recipe implements Spec: mise.toml (as ToolsToml gives it, so the image is
// built from exactly what was hashed) and mise.lock go to ~/toolchain, and
// mise installs their tools on the base image, which is mirrored into the
// cluster's registry once.
func (in Inputs) Recipe(string) (Recipe, error) {
	if err := in.Validate(); err != nil {
		return Recipe{}, err
	}
	files := map[string][]byte{"mise.toml": in.ToolsToml()}
	if len(in.MiseLock) > 0 {
		files["mise.lock"] = in.MiseLock
	}
	return Recipe{
		Image: in.Base, Base: in.Base, Mirror: mirrorRepository(in.Base),
		Dir: toolchainDir, Files: files,
		Run: "mise trust && mise install",
		// mise's download cache stays out of the image.
		RunEnv:      map[string]string{"MISE_DATA_DIR": agentHomeMise, "MISE_CACHE_DIR": "/tmp/mise-cache"},
		ImageEnv:    map[string]string{"MISE_DATA_DIR": agentHomeMise, "MISE_TRUSTED_CONFIG_PATHS": "/workspace"},
		PrependPath: agentHomeMise + "/shims",
		CreatedBy:   "frankie toolchain: mise install",
	}, nil
}

// mirrorRepository is where a base image is mirrored on the push host:
// base/ and the last path element of its repository, e.g. base/frankie-runner.
func mirrorRepository(ref string) string {
	repo := ref
	if i := strings.Index(repo, "@"); i >= 0 {
		repo = repo[:i]
	}
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo = repo[:i]
	}
	r := "base/" + strings.ToLower(path.Base(repo))
	if !repositoryRE.MatchString(r) {
		return "base/runner"
	}
	return r
}
