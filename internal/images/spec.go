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
	// Recipe says how the image is built. pushHost is the cluster-internal
	// registry host, where recorded images are read from.
	Recipe(pushHost string) (Recipe, error)
}

// Recipe is how an image is built by appending one layer (D81): Run runs in
// Image as the agent user, from Dir holding Files, and everything it creates
// or changes becomes a layer appended to Base, with the environment set.
type Recipe struct {
	// Image is what the build pod runs, as nodes pull it.
	Image string
	// Base is the image the layer is appended to, as build pods reach it:
	// Image itself, by the reference the build pod resolves.
	Base string
	// Mirror, when set, is the repository on the push host that Base is
	// copied into once, by digest, so every image built on it shares its
	// layers there.
	Mirror string
	// Dir is where Files are placed and Run runs.
	Dir   string
	Files map[string][]byte
	Run   string
	// RunEnv is set for Run only.
	RunEnv map[string]string
	// ImageEnv is set in the image, and PrependPath put in front of its PATH.
	ImageEnv    map[string]string
	PrependPath string
	// CreatedBy is the layer's history entry, for people inspecting the image.
	CreatedBy string
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
