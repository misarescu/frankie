package images

import (
	"archive/tar"
	"bytes"
	"fmt"
	"maps"
	"slices"
)

// Spec is one image the factory builds: a toolchain image (Inputs) or a
// dependency image (Dependencies). Its tag is a pure function of what it is
// built from, so an image is reused exactly when its inputs are unchanged.
type Spec interface {
	Validate() error
	// Tag names the image in the registry and keys its recorded digest (D45).
	Tag() string
	// Context returns the build context by slash-separated path, Dockerfile
	// included. pushHost is the cluster-internal registry host the build pulls
	// recorded images from; args are the build arguments RUN steps see.
	Context(pushHost string, args []string) (map[string][]byte, error)
}

// maxContextBytes keeps the build context, a tar archive in a ConfigMap,
// under the 1 MiB ConfigMap limit with room for tar headers.
const maxContextBytes = 768 << 10

// contextTar packs a build context into a tar archive with stable order and
// metadata, so the same context always yields the same bytes.
func contextTar(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		b := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg, Format: tar.FormatPAX}); err != nil {
			return nil, fmt.Errorf("images: build context %s: %w", name, err)
		}
		if _, err := tw.Write(b); err != nil {
			return nil, fmt.Errorf("images: build context %s: %w", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("images: build context: %w", err)
	}
	if buf.Len() > maxContextBytes {
		return nil, fmt.Errorf("images: build context is %d bytes, the limit is %d", buf.Len(), maxContextBytes)
	}
	return buf.Bytes(), nil
}
