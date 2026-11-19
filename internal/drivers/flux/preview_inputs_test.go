package flux

import (
	"slices"
	"strings"
	"testing"
)

// emptyInputs is Goodreads-CD's apps/goodreads/previews/inputs.yaml as created.
const emptyInputs = `# Written by Frankie's flux-preview driver (D124): one entry per change request
# with a preview, {changeRequest, tag, sha}.

apiVersion: fluxcd.controlplane.io/v1
kind: ResourceSet
metadata:
  name: goodreads-previews
  namespace: flux-system
spec:
  inputs: []
`

// M7, D125: the driver rewrites the inputs whole, ordered by change request,
// keeping the leading comment and the rest of the document.
func TestInputsRoundTrip(t *testing.T) {
	f, err := parseInputs([]byte(emptyInputs))
	if err != nil || len(f.entries) != 0 {
		t.Fatalf("parseInputs = %+v, %v", f, err)
	}
	entries := []previewEntry{
		{ChangeRequest: "153", Tag: "sha-bbbbbbb", SHA: "bbbbbbbbbb"},
		{ChangeRequest: "17", Tag: "sha-aaaaaaa", SHA: "aaaaaaaaaa"},
	}
	out, err := f.render(entries)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "# Written by Frankie's flux-preview driver (D124): one entry per change request\n# with a preview, {changeRequest, tag, sha}.\n\napiVersion: fluxcd.controlplane.io/v1\n") ||
		!strings.Contains(s, "name: goodreads-previews") || !strings.Contains(s, "namespace: flux-system") ||
		strings.Index(s, `"17"`) > strings.Index(s, `"153"`) {
		t.Errorf("rendered:\n%s", s)
	}
	back, err := parseInputs(out)
	if err != nil || !slices.Equal(back.entries, []previewEntry{entries[1], entries[0]}) {
		t.Errorf("read back = %+v, %v", back.entries, err)
	}
	if e, ok := back.entry(153); !ok || e.Tag != "sha-bbbbbbb" {
		t.Errorf("entry(153) = %+v, %v", e, ok)
	}
	// Rendering does not change the file it was read from.
	if again, err := back.render(nil); err != nil || !strings.Contains(string(again), "inputs: []") || len(back.entries) != 2 {
		t.Errorf("render(nil) = %s, %v; entries %v", again, err, back.entries)
	}
}

// Entries end up in the manifests the ResourceSet renders: anything the
// driver would not have written is refused.
func TestParseInputsRejects(t *testing.T) {
	entry := func(e string) string { return strings.Replace(emptyInputs, "inputs: []", "inputs:\n    - "+e, 1) }
	tests := map[string]struct {
		content, want string
	}{
		"not yaml":       {"kind: [", "inputs file"},
		"not a set":      {strings.Replace(emptyInputs, "kind: ResourceSet", "kind: ConfigMap", 1), "not a ResourceSet"},
		"no spec":        {"kind: ResourceSet\n", "no spec"},
		"number":         {entry(`{changeRequest: "x1", tag: sha-a, sha: aaaaaaa}`), "not a change request number"},
		"zero":           {entry(`{changeRequest: "007", tag: sha-a, sha: aaaaaaa}`), "not a change request number"},
		"tag":            {entry(`{changeRequest: "1", tag: "a: b", sha: aaaaaaa}`), "not an image tag"},
		"sha":            {entry(`{changeRequest: "1", tag: sha-a, sha: "<< inputs.x >>"}`), "not a commit"},
		"unknown key":    {entry(`{changeRequest: "1", tag: sha-a, sha: aaaaaaa, image: evil}`), "unknown field"},
		"two for one CR": {strings.Replace(entry(`{changeRequest: "1", tag: sha-a, sha: aaaaaaa}`), "    - ", "    - {changeRequest: \"1\", tag: sha-b, sha: bbbbbbb}\n    - ", 1), "two entries"},
		"slot":           {entry(`{changeRequest: "1", tag: sha-a, sha: aaaaaaa, slot: "10"}`), "not a slot"},
		"slot number":    {entry(`{changeRequest: "1", tag: sha-a, sha: aaaaaaa, slot: 1}`), "cannot unmarshal"},
		"two in a slot":  {strings.Replace(entry(`{changeRequest: "1", tag: sha-a, sha: aaaaaaa, slot: "0"}`), "    - ", "    - {changeRequest: \"2\", tag: sha-b, sha: bbbbbbb, slot: \"0\"}\n    - ", 1), "two entries in slot 0"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseInputs([]byte(tt.content)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parseInputs = %v, want %q", err, tt.want)
			}
		})
	}
}
