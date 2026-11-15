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

// M6 (D84): collection lists the images on record and forgets deleted ones.
func TestToolchainDigestsListAndForget(t *testing.T) {
	d := ToolchainDigests{Store: newTestStore(t)}
	ctx := t.Context()
	a := "sha256:" + strings.Repeat("a", 64)
	for _, r := range [][2]string{{"toolchains/o/r", "tc-1"}, {"toolchains/o/r", "tc-2"}, {"dependencies/o/r", "dep-1"}} {
		if err := d.Record(ctx, r[0], r[1], a); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.List(ctx)
	if err != nil || len(got) != 3 {
		t.Fatalf("List = %v, %v", got, err)
	}
	if got[0].Repository != "dependencies/o/r" || got[0].Digest != a || got[0].BuiltAt.IsZero() {
		t.Errorf("first = %+v", got[0])
	}
	if err := d.Forget(ctx, "toolchains/o/r", "tc-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := d.Lookup(ctx, "toolchains/o/r", "tc-1"); found {
		t.Error("forgotten image still on record")
	}
	if _, found, _ := d.Lookup(ctx, "toolchains/o/r", "tc-2"); !found {
		t.Error("another tag was forgotten too")
	}
	if err := d.Forget(ctx, "toolchains/o/r", "tc-1"); err != nil {
		t.Errorf("forgetting twice: %v", err)
	}
}
