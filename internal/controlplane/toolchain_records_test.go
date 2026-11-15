package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/store"
)

type fakeToolchainImages struct {
	rows      []store.ToolchainImage
	err       error
	forgotten []string
}

func (f *fakeToolchainImages) List(context.Context) ([]store.ToolchainImage, error) {
	return f.rows, f.err
}
func (f *fakeToolchainImages) Forget(_ context.Context, repo, tag string) error {
	f.forgotten = append(f.forgotten, repo+":"+tag)
	return f.err
}

// M6 (D84): collection reads and forgets the images on record.
func TestToolchainRecords(t *testing.T) {
	at := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	src := &fakeToolchainImages{rows: []store.ToolchainImage{{Repository: "toolchains/o/r", Tag: "tc-1", Digest: "sha256:x", BuiltAt: at}}}
	r := toolchainRecords{src}
	got, err := r.List(t.Context())
	if err != nil || len(got) != 1 || got[0].Repository != "toolchains/o/r" || got[0].Tag != "tc-1" || got[0].Digest != "sha256:x" || !got[0].BuiltAt.Equal(at) {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if err := r.Forget(t.Context(), "toolchains/o/r", "tc-1"); err != nil || len(src.forgotten) != 1 || src.forgotten[0] != "toolchains/o/r:tc-1" {
		t.Errorf("Forget: %v, forgotten %v", err, src.forgotten)
	}
	src.err = errors.New("down")
	if _, err := r.List(t.Context()); err == nil {
		t.Error("a store error was swallowed")
	}
}
