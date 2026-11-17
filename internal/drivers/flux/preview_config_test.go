package flux

import (
	"encoding/json"
	"strings"
	"testing"
)

const goodPreviewConfig = `{"repo":"misarescu/Goodreads-CD","path":"apps/goodreads/previews/inputs.yaml",
 "versionURL":"http://goodreads-previews.stages.svc.cluster.local/pr-{changeRequest}/api/version"}`

// M7, D125.
func TestParsePreviewConfig(t *testing.T) {
	c, err := parsePreviewConfig(json.RawMessage(goodPreviewConfig))
	if err != nil {
		t.Fatal(err)
	}
	if c.Branch != "main" || c.Tag != "sha-{short}" || c.VersionField != "version" || c.MaxPreviews != 2 {
		t.Errorf("defaults = %+v", c)
	}
	if got := c.versionURL(152); got != "http://goodreads-previews.stages.svc.cluster.local/pr-152/api/version" {
		t.Errorf("versionURL(152) = %q", got)
	}
}

func TestParsePreviewConfigRejects(t *testing.T) {
	base := map[string]any{}
	_ = json.Unmarshal([]byte(goodPreviewConfig), &base)
	tests := map[string]struct {
		key  string
		val  any
		want string
	}{
		"repo":            {"repo", "Goodreads-CD", "owner/name"},
		"path":            {"path", "../inputs.yaml", "clean relative path"},
		"tag template":    {"tag", "latest", "neither {sha} nor {short}"},
		"no number":       {"versionURL", "http://x/api/version", "names no {changeRequest}"},
		"version url":     {"versionURL", "ftp://x/pr-{changeRequest}", "http(s) URL"},
		"field":           {"versionField", "a.b", "field name"},
		"too few":         {"maxPreviews", -1, "between 1 and 10"},
		"too many":        {"maxPreviews", 11, "between 1 and 10"},
		"image not taken": {"image", "ghcr.io/x/y", "unknown field"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			m := map[string]any{}
			for k, v := range base {
				m[k] = v
			}
			m[tt.key] = tt.val
			raw, _ := json.Marshal(m)
			if _, err := parsePreviewConfig(raw); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parsePreviewConfig = %v, want %q", err, tt.want)
			}
		})
	}
}
