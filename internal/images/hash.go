// Package images decides which toolchain image an agent step or gate runs in,
// and builds it when the registry does not have it yet (D15, D30,
// docs/spec/05-sandbox-and-toolchains.md). The image is a pure function of the
// platform base image and the repo's mise.toml/mise.lock, so an unchanged
// toolchain is reused instead of rebuilt.
package images

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
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

// Hash is the hex SHA-256 of the inputs. Fields are length-prefixed so moving
// bytes between them always changes the hash.
func (in Inputs) Hash() string {
	h := sha256.New()
	for _, f := range [][]byte{[]byte(in.Base), in.MiseToml, in.MiseLock} {
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
