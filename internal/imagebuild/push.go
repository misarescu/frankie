package imagebuild

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// PushOptions describe the image Push writes.
type PushOptions struct {
	// Base is the image the layer is appended to.
	Base string
	// Mirror, when set, is a repository Base is copied into first, by
	// digest, and appended to from there: a base image from outside the
	// cluster is uploaded once, and every image built on it shares its layers
	// in the registry. The digest is Base's own, so an image another build
	// pushed into Mirror cannot stand in for it. A tag on Mirror
	// (repository:tag) is set on the copy, so registry collection can tell the
	// deployed base's mirror from older ones (D84); nothing builds from it.
	Mirror string
	// Layer is the tar archive Layer wrote.
	Layer string
	// Target is the repository the image is pushed to, by digest only (D45).
	Target string
	// Env are KEY=VALUE pairs set in the image's environment.
	Env []string
	// PrependPath is put in front of the image's PATH; empty leaves it.
	PrependPath string
	// Insecure are registry hosts reached over plain HTTP.
	Insecure []string
	// Platform picks the image from a multi-platform Base.
	Platform v1.Platform
	// CreatedBy is the layer's history entry.
	CreatedBy string
	// Log receives progress; nil discards it.
	Log io.Writer
	// Transport is the HTTP transport; nil means remote.DefaultTransport,
	// which honors the proxy environment.
	Transport http.RoundTripper
}

// Push appends the layer to the base image, sets its environment and pushes
// the result by digest. It returns the digest.
func Push(ctx context.Context, o PushOptions) (v1.Hash, error) {
	if o.Base == "" || o.Layer == "" || o.Target == "" {
		return v1.Hash{}, errors.New("imagebuild: base, layer and target are required")
	}
	logw := o.Log
	if logw == nil {
		logw = io.Discard
	}
	opts := []remote.Option{remote.WithContext(ctx), remote.WithPlatform(o.Platform)}
	if o.Transport != nil {
		opts = append(opts, remote.WithTransport(o.Transport))
	}

	start := time.Now()
	base, err := o.image(o.Base, opts)
	if err != nil {
		return v1.Hash{}, err
	}
	if o.Mirror != "" {
		if base, err = o.mirror(base, opts, logw); err != nil {
			return v1.Hash{}, err
		}
	}
	_, _ = fmt.Fprintf(logw, "imagebuild: base image ready in %s\n", time.Since(start).Round(time.Second))

	img, err := o.append(base)
	if err != nil {
		return v1.Hash{}, err
	}
	digest, err := img.Digest()
	if err != nil {
		return v1.Hash{}, fmt.Errorf("imagebuild: image digest: %w", err)
	}
	target, err := o.ref(o.Target + "@" + digest.String())
	if err != nil {
		return v1.Hash{}, err
	}
	start = time.Now()
	if err := remote.Write(target, img, opts...); err != nil {
		return v1.Hash{}, fmt.Errorf("imagebuild: push %s: %w", target, err)
	}
	_, _ = fmt.Fprintf(logw, "imagebuild: pushed %s in %s\n", target, time.Since(start).Round(time.Second))
	return digest, nil
}

func (o PushOptions) ref(s string) (name.Reference, error) {
	r, err := name.ParseReference(s)
	if err != nil {
		return nil, fmt.Errorf("imagebuild: %w", err)
	}
	if slices.Contains(o.Insecure, r.Context().RegistryStr()) {
		if r, err = name.ParseReference(s, name.Insecure); err != nil {
			return nil, fmt.Errorf("imagebuild: %w", err)
		}
	}
	return r, nil
}

func (o PushOptions) image(s string, opts []remote.Option) (v1.Image, error) {
	r, err := o.ref(s)
	if err != nil {
		return nil, err
	}
	img, err := remote.Image(r, opts...)
	if err != nil {
		return nil, fmt.Errorf("imagebuild: base image %s: %w", s, err)
	}
	return img, nil
}

// mirror copies base into the mirror repository unless it is there already,
// tags it when the mirror names a tag, and returns it from there.
func (o PushOptions) mirror(base v1.Image, opts []remote.Option, logw io.Writer) (v1.Image, error) {
	digest, err := base.Digest()
	if err != nil {
		return nil, fmt.Errorf("imagebuild: base image digest: %w", err)
	}
	repository, tag := splitTag(o.Mirror)
	ref, err := o.ref(repository + "@" + digest.String())
	if err != nil {
		return nil, err
	}
	_, err = remote.Head(ref, opts...)
	var terr *transport.Error
	switch {
	case err == nil:
	case errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound:
		_, _ = fmt.Fprintf(logw, "imagebuild: copying the base image to %s\n", ref)
		if err := remote.Write(ref, base, opts...); err != nil {
			return nil, fmt.Errorf("imagebuild: copy base image to %s: %w", ref, err)
		}
	default:
		return nil, fmt.Errorf("imagebuild: check %s: %w", ref, err)
	}
	img, err := remote.Image(ref, opts...)
	if err != nil {
		return nil, fmt.Errorf("imagebuild: base image %s: %w", ref, err)
	}
	if tag != "" {
		t, err := o.ref(repository + ":" + tag)
		if err != nil {
			return nil, err
		}
		if err := remote.Tag(t.Context().Tag(tag), img, opts...); err != nil {
			return nil, fmt.Errorf("imagebuild: tag base image %s: %w", t, err)
		}
	}
	return img, nil
}

// splitTag splits repository:tag; a repository without a tag returns "".
func splitTag(ref string) (repository, tag string) {
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// append adds the layer, in the base manifest's media type family, and the
// environment.
func (o PushOptions) append(base v1.Image) (v1.Image, error) {
	mt, err := base.MediaType()
	if err != nil {
		return nil, fmt.Errorf("imagebuild: base image: %w", err)
	}
	layerType := types.DockerLayer
	if mt == types.OCIManifestSchema1 {
		layerType = types.OCILayer
	}
	layer, err := tarball.LayerFromFile(o.Layer, tarball.WithMediaType(layerType))
	if err != nil {
		return nil, fmt.Errorf("imagebuild: layer: %w", err)
	}
	img, err := mutate.Append(base, mutate.Addendum{
		Layer:   layer,
		History: v1.History{CreatedBy: o.CreatedBy, Created: v1.Time{Time: time.Now().UTC()}},
	})
	if err != nil {
		return nil, fmt.Errorf("imagebuild: append layer: %w", err)
	}
	if len(o.Env) == 0 && o.PrependPath == "" {
		return img, nil
	}
	cf, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("imagebuild: image config: %w", err)
	}
	cfg := *cf.Config.DeepCopy()
	if o.PrependPath != "" {
		path := "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
		if v, ok := lookupEnv(cfg.Env, "PATH"); ok {
			path = v
		}
		cfg.Env = setEnv(cfg.Env, "PATH="+o.PrependPath+":"+path)
	}
	for _, kv := range o.Env {
		if !strings.Contains(kv, "=") {
			return nil, fmt.Errorf("imagebuild: env %q is not KEY=VALUE", kv)
		}
		cfg.Env = setEnv(cfg.Env, kv)
	}
	img, err = mutate.Config(img, cfg)
	if err != nil {
		return nil, fmt.Errorf("imagebuild: image config: %w", err)
	}
	return img, nil
}

func lookupEnv(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			return v, true
		}
	}
	return "", false
}

// setEnv replaces the variable kv names, or adds it.
func setEnv(env []string, kv string) []string {
	key, _, _ := strings.Cut(kv, "=")
	out := slices.Clone(env)
	for i, e := range out {
		if k, _, _ := strings.Cut(e, "="); k == key {
			out[i] = kv
			return out
		}
	}
	return append(out, kv)
}
