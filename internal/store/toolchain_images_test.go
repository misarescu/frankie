package store

import (
	"strings"
	"testing"
)

func TestToolchainDigests(t *testing.T) {
	d := ToolchainDigests{Store: newTestStore(t)}
	ctx := t.Context()
	a, b := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)

	if _, found, err := d.Lookup(ctx, "toolchains/o/r", "tc-1"); err != nil || found {
		t.Fatalf("empty lookup: found %v, err %v", found, err)
	}
	if err := d.Record(ctx, "toolchains/o/r", "tc-1", a); err != nil {
		t.Fatal(err)
	}
	if got, found, err := d.Lookup(ctx, "toolchains/o/r", "tc-1"); err != nil || !found || got != a {
		t.Fatalf("lookup = %q, %v, %v", got, found, err)
	}
	// Another repository's image of the same tag is a different record.
	if _, found, _ := d.Lookup(ctx, "toolchains/o/other", "tc-1"); found {
		t.Error("digest leaked across repositories")
	}
	if err := d.Record(ctx, "toolchains/o/r", "tc-1", b); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := d.Lookup(ctx, "toolchains/o/r", "tc-1"); got != b {
		t.Errorf("after a rebuild = %q, want %q", got, b)
	}
	if err := d.Record(ctx, "toolchains/o/r", "tc-2", "latest"); err == nil {
		t.Error("a non-digest was recorded")
	}
}
