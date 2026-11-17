package flux

import (
	"encoding/json"
	"strings"
	"testing"
)

const goodConfig = `{"repo":"misarescu/Goodreads-CD","path":"apps/goodreads/staging/kustomization.yaml",
 "image":"ghcr.io/misarescu/goodreads-clone","versionURL":"http://goodreads-stg.192.168.3.7.nip.io/api/version"}`

// M7, D117.
func TestParseConfig(t *testing.T) {
	c, err := parseConfig(json.RawMessage(goodConfig))
	if err != nil {
		t.Fatal(err)
	}
	if c.Branch != "main" || c.Tag != "sha-{short}" || c.VersionField != "version" {
		t.Errorf("defaults = %+v", c)
	}
	if got := renderTag(c.Tag, "a44235f019e72d7fbf789982bc7b3557f5865597"); got != "sha-a44235f" {
		t.Errorf("tag = %q", got)
	}
	if got := renderTag("v-{sha}", "abc"); got != "v-abc" {
		t.Errorf("full sha tag = %q", got)
	}
}

func TestParseConfigRejects(t *testing.T) {
	base := map[string]any{}
	_ = json.Unmarshal([]byte(goodConfig), &base)
	tests := map[string]struct {
		key  string
		val  any
		want string
	}{
		"repo":          {"repo", "Goodreads-CD", "owner/name"},
		"branch":        {"branch", "-x", "branch name"},
		"absolute path": {"path", "/apps/k.yaml", "clean relative path"},
		"outside path":  {"path", "../k.yaml", "clean relative path"},
		"not yaml":      {"path", "apps/k.json", "clean relative path"},
		"image":         {"image", "a b", "image name"},
		"tag template":  {"tag", "latest", "neither {sha} nor {short}"},
		"tag chars":     {"tag", "{short}:x", "does not render"},
		"version url":   {"versionURL", "ftp://x", "http(s) URL"},
		"credentials":   {"versionURL", "https://u:p@x/v", "without credentials"},
		"field":         {"versionField", "a.b", "field name"},
		"unknown":       {"token", "x", "unknown field"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			m := map[string]any{}
			for k, v := range base {
				m[k] = v
			}
			m[tt.key] = tt.val
			raw, _ := json.Marshal(m)
			if _, err := parseConfig(raw); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parseConfig = %v, want %q", err, tt.want)
			}
		})
	}
}
