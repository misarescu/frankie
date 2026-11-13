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
