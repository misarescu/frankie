package images

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeRegistry holds manifests by repository@digest and records tags.
type fakeRegistry struct {
	manifests map[string]bool
	tags      map[string]string // repository:tag -> digest
	err       error
}

func (f *fakeRegistry) HasManifest(_ context.Context, repository, digest string) (bool, error) {
	return f.manifests[repository+"@"+digest], f.err
}

func (f *fakeRegistry) Tag(_ context.Context, repository, digest, tag string) error {
	f.tags[repository+":"+tag] = digest
	return nil
}

type memDigests map[string]string

func (m memDigests) Lookup(_ context.Context, repository, tag string) (string, bool, error) {
	d, ok := m[repository+":"+tag]
	return d, ok, nil
}

func (m memDigests) Record(_ context.Context, repository, tag, digest string) error {
	m[repository+":"+tag] = digest
	return nil
}

// fakeBuilder pushes a manifest by digest, the way a build does.
type fakeBuilder struct {
	registry *fakeRegistry
	builds   []BuildRequest
	err      error
	report   string // overrides the digest the build reports
}

func (f *fakeBuilder) Build(_ context.Context, r BuildRequest) (string, error) {
	f.builds = append(f.builds, r)
	if f.err != nil {
		return "", f.err
	}
	digest := fmt.Sprintf("sha256:%064x", len(f.builds))
	f.registry.manifests[r.Repository+"@"+digest] = true
	if f.report != "" {
		return f.report, nil
	}
	return digest, nil
}

func newEnsurer() (Ensurer, *fakeBuilder, *fakeRegistry, memDigests) {
	reg := &fakeRegistry{manifests: map[string]bool{}, tags: map[string]string{}}
	b := &fakeBuilder{registry: reg}
	d := memDigests{}
	return Ensurer{Registry: reg, Builder: b, Digests: d, PullHost: "localhost:30500"}, b, reg, d
}

var testInputs = Inputs{Base: "ghcr.io/x/runner:sha-1", MiseToml: []byte("[tools]\ngo = \"1.27\"\n")}

// An unchanged mise.toml reuses the image instead of rebuilding it (M2 done-when),
// and pods get it by digest (D45).
func TestEnsureReusesImageForUnchangedToolchain(t *testing.T) {
	e, b, reg, d := newEnsurer()
	first, err := e.Ensure(context.Background(), "toolchains/misarescu/repo", testInputs)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("sha256:%064x", 1)
	if !first.Built || len(b.builds) != 1 || first.Ref != "localhost:30500/toolchains/misarescu/repo@"+digest {
		t.Fatalf("first = %+v, builds = %d", first, len(b.builds))
	}
	if d["toolchains/misarescu/repo:"+testInputs.Tag()] != digest || reg.tags["toolchains/misarescu/repo:"+testInputs.Tag()] != digest {
		t.Errorf("recorded %v, tagged %v", d, reg.tags)
	}

	second, err := e.Ensure(context.Background(), "toolchains/misarescu/repo", testInputs)
	if err != nil {
		t.Fatal(err)
	}
	if second.Built || len(b.builds) != 1 || second.Ref != first.Ref {
		t.Fatalf("second = %+v, builds = %d; want the recorded image", second, len(b.builds))
	}
}

// Open question 15: a tag in the registry proves nothing. An image someone
// pushed under the tag is never used; only a build on record is.
func TestEnsureIgnoresTagsItDidNotRecord(t *testing.T) {
	e, b, reg, _ := newEnsurer()
	reg.tags["toolchains/o/r:"+testInputs.Tag()] = "sha256:" + strings.Repeat("e", 64) // pushed by another build pod
	reg.manifests["toolchains/o/r@sha256:"+strings.Repeat("e", 64)] = true
	got, err := e.Ensure(context.Background(), "toolchains/o/r", testInputs)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Built || len(b.builds) != 1 || strings.Contains(got.Ref, strings.Repeat("e", 64)) {
		t.Fatalf("image = %+v; want a build of its own", got)
	}
}

func TestEnsureRebuildsWhenTheRegistryLostTheImage(t *testing.T) {
	e, b, reg, _ := newEnsurer()
	if _, err := e.Ensure(context.Background(), "toolchains/o/r", testInputs); err != nil {
		t.Fatal(err)
	}
	clear(reg.manifests) // the registry's storage was reset
	got, err := e.Ensure(context.Background(), "toolchains/o/r", testInputs)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Built || len(b.builds) != 2 || !strings.HasSuffix(got.Ref, fmt.Sprintf("@sha256:%064x", 2)) {
		t.Fatalf("image = %+v, builds = %d", got, len(b.builds))
	}
}

func TestEnsureRebuildsWhenInputsChange(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Inputs)
	}{
		{"mise.toml", func(in *Inputs) { in.MiseToml = []byte("[tools]\ngo = \"1.28\"\n") }},
		{"mise.lock", func(in *Inputs) { in.MiseLock = []byte("new lock") }},
		{"base image", func(in *Inputs) { in.Base = "ghcr.io/x/runner:sha-2" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, b, _, _ := newEnsurer()
			in := testInputs
			if _, err := e.Ensure(context.Background(), "toolchains/o/r", in); err != nil {
				t.Fatal(err)
			}
			tt.change(&in)
			got, err := e.Ensure(context.Background(), "toolchains/o/r", in)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Built || len(b.builds) != 2 {
				t.Fatalf("changed %s: built = %v, builds = %d; want a second build", tt.name, got.Built, len(b.builds))
			}
		})
	}
}

func TestEnsureErrors(t *testing.T) {
	t.Run("registry unreachable does not use the record blindly", func(t *testing.T) {
		e, _, reg, d := newEnsurer()
		d["toolchains/o/r:"+testInputs.Tag()] = fmt.Sprintf("sha256:%064x", 9)
		reg.err = errors.New("connection refused")
		if _, err := e.Ensure(context.Background(), "toolchains/o/r", testInputs); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("build failure is returned", func(t *testing.T) {
		e, b, _, _ := newEnsurer()
		b.err = errors.New("boom")
		if _, err := e.Ensure(context.Background(), "toolchains/o/r", testInputs); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want boom", err)
		}
	})
	t.Run("a reported digest that is not a digest", func(t *testing.T) {
		e, b, _, d := newEnsurer()
		b.report = "latest"
		if _, err := e.Ensure(context.Background(), "toolchains/o/r", testInputs); err == nil || len(d) != 0 {
			t.Fatalf("err = %v, recorded %v", err, d)
		}
	})
	t.Run("a reported digest the registry does not hold", func(t *testing.T) {
		e, b, _, d := newEnsurer()
		b.report = "sha256:" + strings.Repeat("f", 64)
		if _, err := e.Ensure(context.Background(), "toolchains/o/r", testInputs); err == nil || !strings.Contains(err.Error(), "not in the registry") || len(d) != 0 {
			t.Fatalf("err = %v, recorded %v", err, d)
		}
	})
	t.Run("invalid inputs", func(t *testing.T) {
		e, _, _, _ := newEnsurer()
		if _, err := e.Ensure(context.Background(), "toolchains/o/r", Inputs{}); err == nil {
			t.Fatal("expected a validation error")
		}
	})
}

// M5.2 (D80): a build gets the proxy URL its caller passes, carrying the
// project's egress token; the image is the same either way.
func TestEnsureViaPassesTheProxyToTheBuild(t *testing.T) {
	e, b, _, _ := newEnsurer()
	const proxy = "http://frankie:feg1.x.y@egress:3128"
	img, err := e.EnsureVia(context.Background(), "toolchains/misarescu/repo", testInputs, proxy)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.builds) != 1 || b.builds[0].EgressProxyURL != proxy {
		t.Fatalf("builds = %+v", b.builds)
	}
	if again, err := e.Ensure(context.Background(), "toolchains/misarescu/repo", testInputs); err != nil || again.Ref != img.Ref || again.Built {
		t.Errorf("without the proxy: %+v, %v; want the same recorded image", again, err)
	}
}

// collectingBuilder tries to start a registry collection mid-build.
type collectingBuilder struct {
	*fakeBuilder
	m         *Maintenance
	collected bool
}

func (c *collectingBuilder) Build(ctx context.Context, r BuildRequest) (string, error) {
	if stop, ok := c.m.TryCollect(); ok {
		c.collected = true
		stop()
	}
	return c.fakeBuilder.Build(ctx, r)
}

// M6 (D84): no registry collection starts while Ensure builds and records an
// image, and Ensure waits for a running one.
func TestEnsureHoldsOffCollection(t *testing.T) {
	e, b, _, _ := newEnsurer()
	m := &Maintenance{}
	cb := &collectingBuilder{fakeBuilder: b, m: m}
	e.Builder, e.Maintenance = cb, m
	if _, err := e.Ensure(t.Context(), "toolchains/misarescu/repo", testInputs); err != nil {
		t.Fatal(err)
	}
	if cb.collected {
		t.Error("a collection started during a build")
	}
	if stop, ok := m.TryCollect(); !ok {
		t.Fatal("Ensure still holds maintenance after it returned")
	} else {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := e.Ensure(ctx, "toolchains/misarescu/repo", testInputs); err == nil {
			t.Error("Ensure ran during a collection")
		}
		stop()
	}
}

// M6 (D93): a request hears when it waits for a build and how it ended, and
// nothing when the image is on record.
func TestEnsureReportsBuildProgress(t *testing.T) {
	e, b, _, _ := newEnsurer()
	p := &progressLog{}
	if _, err := e.EnsureWith(t.Context(), "toolchains/misarescu/repo", testInputs, EnsureOptions{Progress: p}); err != nil {
		t.Fatal(err)
	}
	want := "toolchains/misarescu/repo:" + testInputs.Tag()
	if len(p.building) != 1 || p.building[0] != want || len(p.built) != 1 || p.errs[0] != nil {
		t.Fatalf("progress = %+v", p)
	}
	if _, err := e.EnsureWith(t.Context(), "toolchains/misarescu/repo", testInputs, EnsureOptions{Progress: p}); err != nil || len(p.building) != 1 {
		t.Errorf("a recorded image reported a build: %+v, %v", p, err)
	}
	b.err = errors.New("mise install failed")
	if _, err := e.EnsureWith(t.Context(), "toolchains/misarescu/other", testInputs, EnsureOptions{Progress: p}); err == nil || len(p.errs) != 2 || p.errs[1] == nil {
		t.Errorf("a failed build: %v, %+v", err, p)
	}
}
