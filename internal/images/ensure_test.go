package images

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRegistry struct {
	have map[string]bool // repository:tag
	err  error
}

func (f *fakeRegistry) Has(_ context.Context, repository, tag string) (bool, error) {
	return f.have[repository+":"+tag], f.err
}

// fakeBuilder behaves like a registry: building makes the image exist.
type fakeBuilder struct {
	registry *fakeRegistry
	builds   []BuildRequest
	err      error
}

func (f *fakeBuilder) Build(_ context.Context, r BuildRequest) error {
	f.builds = append(f.builds, r)
	if f.err != nil {
		return f.err
	}
	f.registry.have[r.Repository+":"+r.Inputs.Tag()] = true
	return nil
}

func newEnsurer() (Ensurer, *fakeBuilder) {
	reg := &fakeRegistry{have: map[string]bool{}}
	b := &fakeBuilder{registry: reg}
	return Ensurer{Registry: reg, Builder: b, PullHost: "localhost:30500"}, b
}

// An unchanged mise.toml reuses the image instead of rebuilding it (M2 done-when).
func TestEnsureReusesImageForUnchangedToolchain(t *testing.T) {
	e, b := newEnsurer()
	in := Inputs{Base: "ghcr.io/x/runner:sha-1", MiseToml: []byte("[tools]\ngo = \"1.27\"\n")}

	first, err := e.Ensure(context.Background(), "misarescu/Repo", in)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Built || len(b.builds) != 1 {
		t.Fatalf("first call: built = %v, builds = %d; want one build", first.Built, len(b.builds))
	}
	if want := "localhost:30500/toolchains/misarescu/repo:" + in.Tag(); first.Ref != want {
		t.Errorf("ref = %q, want %q", first.Ref, want)
	}

	second, err := e.Ensure(context.Background(), "misarescu/Repo", in)
	if err != nil {
		t.Fatal(err)
	}
	if second.Built || len(b.builds) != 1 {
		t.Fatalf("second call rebuilt: built = %v, builds = %d", second.Built, len(b.builds))
	}
	if second.Ref != first.Ref {
		t.Errorf("ref changed: %q vs %q", second.Ref, first.Ref)
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
			e, b := newEnsurer()
			in := Inputs{Base: "ghcr.io/x/runner:sha-1", MiseToml: []byte("[tools]\ngo = \"1.27\"\n")}
			if _, err := e.Ensure(context.Background(), "o/r", in); err != nil {
				t.Fatal(err)
			}
			tt.change(&in)
			got, err := e.Ensure(context.Background(), "o/r", in)
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
	in := Inputs{Base: "b", MiseToml: []byte("x")}
	t.Run("registry unreachable does not trigger a build", func(t *testing.T) {
		e, b := newEnsurer()
		e.Registry.(*fakeRegistry).err = errors.New("connection refused")
		if _, err := e.Ensure(context.Background(), "o/r", in); err == nil || len(b.builds) != 0 {
			t.Fatalf("err = %v, builds = %d; want an error and no build", err, len(b.builds))
		}
	})
	t.Run("build failure is returned", func(t *testing.T) {
		e, b := newEnsurer()
		b.err = errors.New("boom")
		if _, err := e.Ensure(context.Background(), "o/r", in); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v, want boom", err)
		}
	})
	t.Run("invalid inputs", func(t *testing.T) {
		e, _ := newEnsurer()
		if _, err := e.Ensure(context.Background(), "o/r", Inputs{}); err == nil {
			t.Fatal("expected a validation error")
		}
	})
}
