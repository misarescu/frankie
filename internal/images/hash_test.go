package images

import (
	"strings"
	"testing"
)

func TestInputsHash(t *testing.T) {
	base := Inputs{Base: "r/base:v1", MiseToml: []byte("[tools]\ngo = \"1.27\"\n"), MiseLock: []byte("lock")}
	tests := []struct {
		name     string
		other    Inputs
		wantSame bool
	}{
		{"identical", base, true},
		{"different base", Inputs{Base: "r/base:v2", MiseToml: base.MiseToml, MiseLock: base.MiseLock}, false},
		{"different mise.toml", Inputs{Base: base.Base, MiseToml: []byte("[tools]\ngo = \"1.28\"\n"), MiseLock: base.MiseLock}, false},
		{"different mise.lock", Inputs{Base: base.Base, MiseToml: base.MiseToml, MiseLock: []byte("other")}, false},
		{"no lock", Inputs{Base: base.Base, MiseToml: base.MiseToml}, false},
		// Without length prefixes these two would hash the same bytes.
		{"bytes moved between fields", Inputs{Base: base.Base, MiseToml: append(append([]byte{}, base.MiseToml...), 'l'), MiseLock: []byte("ock")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := base.Hash() == tt.other.Hash(); got != tt.wantSame {
				t.Fatalf("hash equal = %v, want %v", got, tt.wantSame)
			}
		})
	}
}

func TestInputsTagIsStableAndRegistrySafe(t *testing.T) {
	in := Inputs{Base: "r/base:v1", MiseToml: []byte("x")}
	same := Inputs{Base: "r/base:v1", MiseToml: []byte("x")}
	if in.Tag() != same.Tag() {
		t.Fatal("tag is not deterministic")
	}
	if !strings.HasPrefix(in.Tag(), "tc-") || len(in.Tag()) != 3+32 {
		t.Fatalf("unexpected tag %q", in.Tag())
	}
}

func TestInputsValidate(t *testing.T) {
	big := make([]byte, maxFileSize+1)
	tests := []struct {
		name    string
		in      Inputs
		wantErr string
	}{
		{"ok", Inputs{Base: "b", MiseToml: []byte("x")}, ""},
		{"no base", Inputs{MiseToml: []byte("x")}, "base image"},
		{"no mise.toml", Inputs{Base: "b"}, "mise.toml is required"},
		{"oversized lock", Inputs{Base: "b", MiseToml: []byte("x"), MiseLock: big}, "mise.lock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// D75: editing a task does not change the toolchain; editing a tool does. A
// mise.toml without tasks, or one that does not parse, is used as it is.
func TestToolsToml(t *testing.T) {
	const tools = "# Tool versions\n[tools]\ngo = \"1.25.5\"\nnode = \"24.22.3\"\n\n[settings]\nexperimental = true\n"
	withTasks := func(run string) []byte {
		return []byte(tools + `
[tasks."deps:warm"]
description = "warm the caches"
run = """
go mod download
[ -f go-packages.txt ] && go build $(cat go-packages.txt)
"""

[tasks.test]
run = "` + run + `"

[task_config]
includes = ["tasks"]
`)
	}
	in := func(toml []byte) Inputs { return Inputs{Base: "r/base:v1", MiseToml: toml, MiseLock: []byte("lock")} }

	if got := in([]byte(tools)).ToolsToml(); string(got) != tools {
		t.Errorf("a mise.toml without tasks changed:\n%s", got)
	}
	a, b := in(withTasks("make test")), in(withTasks("go test ./..."))
	if a.Hash() != b.Hash() {
		t.Error("editing a task changed the toolchain hash")
	}
	if a.Hash() == in([]byte(strings.Replace(string(withTasks("make test")), "1.25.5", "1.25.6", 1))).Hash() {
		t.Error("changing a tool version kept the toolchain hash")
	}
	if a.Hash() == in([]byte(strings.Replace(string(withTasks("make test")), "experimental = true", "experimental = false", 1))).Hash() {
		t.Error("changing a setting kept the toolchain hash")
	}
	stripped := string(a.ToolsToml())
	for _, gone := range []string{"tasks", "task_config", "deps:warm", "go mod download"} {
		if strings.Contains(stripped, gone) {
			t.Errorf("ToolsToml kept %q:\n%s", gone, stripped)
		}
	}
	for _, kept := range []string{"[tools]", "go = '1.25.5'", "node = '24.22.3'", "[settings]", "experimental = true"} {
		if !strings.Contains(stripped, kept) {
			t.Errorf("ToolsToml lost %q:\n%s", kept, stripped)
		}
	}
	if bad := in([]byte("[tools\nnot toml")); string(bad.ToolsToml()) != "[tools\nnot toml" {
		t.Error("an unparsable mise.toml was rewritten")
	}

	// The image is built from exactly what was hashed.
	r, err := a.Recipe("")
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Files["mise.toml"]) != stripped {
		t.Errorf("build context mise.toml =\n%s\nwant ToolsToml", r.Files["mise.toml"])
	}
}

// M5.2 (D81): a toolchain image is the base image plus what mise installs,
// with the environment the agent finds its tools by; the base is mirrored
// into the cluster's registry once.
func TestInputsRecipe(t *testing.T) {
	r, err := testInputsSpec().Recipe("frankie-registry.frankie.svc:5000")
	if err != nil {
		t.Fatal(err)
	}
	base := "ghcr.io/misarescu/frankie-runner:sha-abc"
	if r.Image != base || r.Base != base || r.Mirror != "base/frankie-runner" {
		t.Errorf("image %q, base %q, mirror %q", r.Image, r.Base, r.Mirror)
	}
	if r.Dir != "/home/agent/toolchain" || r.Run != "mise trust && mise install" || string(r.Files["mise.lock"]) != "lock" {
		t.Errorf("dir %q, run %q, files %v", r.Dir, r.Run, r.Files)
	}
	if r.ImageEnv["MISE_DATA_DIR"] != "/home/agent/.local/share/mise" || r.ImageEnv["MISE_TRUSTED_CONFIG_PATHS"] != "/workspace" ||
		r.PrependPath != "/home/agent/.local/share/mise/shims" {
		t.Errorf("image env %v, path %q", r.ImageEnv, r.PrependPath)
	}
	// The download cache is scratch, outside the layer.
	if !strings.HasPrefix(r.RunEnv["MISE_CACHE_DIR"], "/tmp/") {
		t.Errorf("run env %v", r.RunEnv)
	}
	if _, err := (Inputs{Base: base}).Recipe(""); err == nil {
		t.Error("a recipe without mise.toml")
	}
}

func TestMirrorRepository(t *testing.T) {
	for ref, want := range map[string]string{
		"ghcr.io/misarescu/frankie-runner:sha-abc":                           "base/frankie-runner",
		"ghcr.io/misarescu/Frankie-Runner@sha256:" + strings.Repeat("a", 64): "base/frankie-runner",
		"localhost:5000/runner":                                              "base/runner",
		"registry:5000/team/factory-base:v1":                                 "base/factory-base",
		"weird/Name With Spaces:1":                                           "base/runner",
	} {
		if got := mirrorRepository(ref); got != want {
			t.Errorf("mirrorRepository(%q) = %q, want %q", ref, got, want)
		}
	}
}
