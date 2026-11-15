package imagebuild

import (
	"archive/tar"
	"bytes"
	"io"
	"log"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

func testRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// pushBase writes a base image with PATH set to ref.
func pushBase(t *testing.T, ref string, mt types.MediaType) v1.Image {
	t.Helper()
	img, err := random.Image(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	img = mutate.MediaType(img, mt)
	img, err = mutate.Config(img, v1.Config{Env: []string{"PATH=/home/agent/.local/bin:/usr/bin", "LANG=C"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := name.ParseReference(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(r, img); err != nil {
		t.Fatal(err)
	}
	return img
}

func layerFile(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for n, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg, Uid: 10001, Gid: 10001}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "layer.tar")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func pulled(t *testing.T, ref string) v1.Image {
	t.Helper()
	r, err := name.ParseReference(ref, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	img, err := remote.Image(r)
	if err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	return img
}

// M5.2 (D81): the pushed image is the base plus one layer holding the
// install, with the toolchain's environment, and it is pushed by digest.
func TestPushAppendsTheLayerAndSetsTheEnvironment(t *testing.T) {
	for _, mt := range []types.MediaType{types.DockerManifestSchema2, types.OCIManifestSchema1} {
		t.Run(string(mt), func(t *testing.T) {
			host := testRegistry(t)
			base := pushBase(t, host+"/upstream/runner:sha-1", mt)
			var log bytes.Buffer
			digest, err := Push(t.Context(), PushOptions{
				Base:        host + "/upstream/runner:sha-1",
				Layer:       layerFile(t, map[string]string{"home/agent/toolchain/mise.toml": "[tools]\n"}),
				Target:      host + "/toolchains/acme/app",
				Env:         []string{"MISE_DATA_DIR=/home/agent/.local/share/mise", "LANG=C.UTF-8"},
				PrependPath: "/home/agent/.local/share/mise/shims",
				Insecure:    []string{host},
				CreatedBy:   "mise install",
				Log:         &log,
			})
			if err != nil {
				t.Fatal(err)
			}
			img := pulled(t, host+"/toolchains/acme/app@"+digest.String())
			got, err := img.Digest()
			if err != nil || got != digest {
				t.Fatalf("pushed digest = %v (%v), reported %v", got, err, digest)
			}
			baseLayers, _ := base.Layers()
			layers, err := img.Layers()
			if err != nil || len(layers) != len(baseLayers)+1 {
				t.Fatalf("layers = %d (%v), want %d", len(layers), err, len(baseLayers)+1)
			}
			for i, l := range baseLayers {
				want, _ := l.Digest()
				if have, _ := layers[i].Digest(); have != want {
					t.Errorf("layer %d = %v, want the base's %v", i, have, want)
				}
			}
			top := layers[len(layers)-1]
			if lmt, _ := top.MediaType(); (mt == types.OCIManifestSchema1) != (lmt == types.OCILayer) {
				t.Errorf("layer media type %s in a %s manifest", lmt, mt)
			}
			rc, err := top.Uncompressed()
			if err != nil {
				t.Fatal(err)
			}
			h, err := tar.NewReader(rc).Next()
			if err != nil || h.Name != "home/agent/toolchain/mise.toml" || h.Uid != 10001 {
				t.Errorf("layer entry = %+v (%v)", h, err)
			}
			_ = rc.Close()

			cf, err := img.ConfigFile()
			if err != nil {
				t.Fatal(err)
			}
			wantEnv := []string{
				"PATH=/home/agent/.local/share/mise/shims:/home/agent/.local/bin:/usr/bin",
				"LANG=C.UTF-8",
				"MISE_DATA_DIR=/home/agent/.local/share/mise",
			}
			if !slices.Equal(cf.Config.Env, wantEnv) {
				t.Errorf("env = %v, want %v", cf.Config.Env, wantEnv)
			}
			if h := cf.History[len(cf.History)-1]; h.CreatedBy != "mise install" {
				t.Errorf("history = %+v", h)
			}
		})
	}
}

// The base image is copied into the mirror by its own digest, once, and the
// image is built from there; whatever another build pushed under the mirror's
// tags is not used.
func TestPushMirrorsTheBaseByDigest(t *testing.T) {
	host := testRegistry(t)
	base := pushBase(t, host+"/upstream/runner:sha-1", types.DockerManifestSchema2)
	baseDigest, _ := base.Digest()
	// A build pod could have pushed anything under a tag in the mirror.
	pushBase(t, host+"/base/runner:sha-1", types.DockerManifestSchema2)

	opts := PushOptions{
		Base: host + "/upstream/runner:sha-1", Mirror: host + "/base/runner",
		Layer:  layerFile(t, map[string]string{"a": "1"}),
		Target: host + "/toolchains/acme/app", Insecure: []string{host},
	}
	var log bytes.Buffer
	opts.Log = &log
	first, err := Push(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log.Bytes(), []byte("copying the base image")) {
		t.Errorf("log = %q, want the base copied", log.String())
	}
	if d, _ := pulled(t, host+"/base/runner@"+baseDigest.String()).Digest(); d != baseDigest {
		t.Errorf("mirror holds %v, want %v", d, baseDigest)
	}
	baseLayers, _ := base.Layers()
	layers, _ := pulled(t, host+"/toolchains/acme/app@"+first.String()).Layers()
	for i, l := range baseLayers {
		want, _ := l.Digest()
		if have, _ := layers[i].Digest(); have != want {
			t.Errorf("layer %d = %v, want the base's %v", i, have, want)
		}
	}

	log.Reset()
	if _, err := Push(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log.Bytes(), []byte("copying the base image")) {
		t.Errorf("log = %q: the base was copied again", log.String())
	}
}

// M6 (D84): a mirror named with a tag gets that tag on the base's copy, also
// when the copy was there already and the tag pointed elsewhere.
func TestPushTagsTheMirror(t *testing.T) {
	host := testRegistry(t)
	base := pushBase(t, host+"/upstream/runner:sha-1", types.DockerManifestSchema2)
	baseDigest, _ := base.Digest()
	other := pushBase(t, host+"/base/runner:sha-1", types.OCIManifestSchema1)
	if d, _ := other.Digest(); d == baseDigest {
		t.Fatal("test images are the same")
	}
	opts := PushOptions{
		Base: host + "/upstream/runner:sha-1", Mirror: host + "/base/runner:sha-1",
		Layer:  layerFile(t, map[string]string{"a": "1"}),
		Target: host + "/toolchains/acme/app", Insecure: []string{host},
	}
	for range 2 {
		if _, err := Push(t.Context(), opts); err != nil {
			t.Fatal(err)
		}
		if d, _ := pulled(t, host+"/base/runner:sha-1").Digest(); d != baseDigest {
			t.Errorf("mirror tag = %v, want the base %v", d, baseDigest)
		}
	}
}

func TestPushFailsWithoutTheBase(t *testing.T) {
	host := testRegistry(t)
	_, err := Push(t.Context(), PushOptions{
		Base: host + "/upstream/runner:missing", Layer: layerFile(t, map[string]string{"a": "1"}),
		Target: host + "/toolchains/acme/app", Insecure: []string{host},
	})
	if err == nil {
		t.Fatal("push without a base image succeeded")
	}
}

func TestPushRejectsMalformedEnv(t *testing.T) {
	host := testRegistry(t)
	pushBase(t, host+"/upstream/runner:sha-1", types.DockerManifestSchema2)
	_, err := Push(t.Context(), PushOptions{
		Base: host + "/upstream/runner:sha-1", Layer: layerFile(t, map[string]string{"a": "1"}),
		Target: host + "/toolchains/acme/app", Insecure: []string{host}, Env: []string{"NOEQUALS"},
	})
	if err == nil {
		t.Fatal("malformed env accepted")
	}
}
