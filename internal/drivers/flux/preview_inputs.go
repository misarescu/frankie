package flux

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// previewEntry is one change request's preview in the inputs file (D125):
// the inputs the GitOps repository's ResourceSet renders a preview from.
// Values are strings, which ResourceSet templates take as they are.
type previewEntry struct {
	ChangeRequest string `json:"changeRequest"`
	Tag           string `json:"tag"`
	SHA           string `json:"sha"`
}

// number is the entry's change request number.
func (e previewEntry) number() int {
	n, _ := strconv.Atoi(e.ChangeRequest)
	return n
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// check rejects an entry the driver would not have written: its values end up
// in the manifests the ResourceSet renders.
func (e previewEntry) check() error {
	n, err := strconv.Atoi(e.ChangeRequest)
	switch {
	case err != nil || n < 1 || strconv.Itoa(n) != e.ChangeRequest:
		return fmt.Errorf("changeRequest %q is not a change request number", e.ChangeRequest)
	case !tagRE.MatchString(e.Tag):
		return fmt.Errorf("entry %s: tag %q is not an image tag", e.ChangeRequest, e.Tag)
	case !shaRE.MatchString(e.SHA):
		return fmt.Errorf("entry %s: sha %q is not a commit", e.ChangeRequest, e.SHA)
	}
	return nil
}

// inputsFile is the file the flux-preview driver writes: a ResourceSet
// document (a patch over the repository's ResourceSet) whose spec.inputs are
// the entries. The driver rewrites it whole, keeping its leading comment and
// every field but spec.inputs.
type inputsFile struct {
	header  string
	doc     map[string]any
	entries []previewEntry
}

// parseInputs reads an inputs file.
func parseInputs(content []byte) (inputsFile, error) {
	var f inputsFile
	lines := strings.SplitAfter(string(content), "\n")
	i := 0
	for ; i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasPrefix(t, "#") {
			break
		}
	}
	f.header = strings.Join(lines[:i], "")
	if err := yaml.Unmarshal(content, &f.doc); err != nil {
		return inputsFile{}, fmt.Errorf("flux-preview: inputs file: %w", err)
	}
	if f.doc["kind"] != "ResourceSet" {
		return inputsFile{}, errors.New("flux-preview: the inputs file is not a ResourceSet")
	}
	spec, ok := f.doc["spec"].(map[string]any)
	if !ok {
		return inputsFile{}, errors.New("flux-preview: the inputs file has no spec")
	}
	raw, err := json.Marshal(spec["inputs"])
	if err != nil {
		return inputsFile{}, fmt.Errorf("flux-preview: inputs: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f.entries); err != nil {
		return inputsFile{}, fmt.Errorf("flux-preview: inputs: %w", err)
	}
	seen := map[int]bool{}
	for _, e := range f.entries {
		if err := e.check(); err != nil {
			return inputsFile{}, fmt.Errorf("flux-preview: inputs: %w", err)
		}
		if seen[e.number()] {
			return inputsFile{}, fmt.Errorf("flux-preview: inputs: two entries for change request %s", e.ChangeRequest)
		}
		seen[e.number()] = true
	}
	return f, nil
}

// entry returns change request n's entry.
func (f inputsFile) entry(n int) (previewEntry, bool) {
	i := slices.IndexFunc(f.entries, func(e previewEntry) bool { return e.number() == n })
	if i < 0 {
		return previewEntry{}, false
	}
	return f.entries[i], true
}

// render writes the file with entries in place of its own, ordered by change
// request; it reads the result back to check it says exactly that.
func (f inputsFile) render(entries []previewEntry) ([]byte, error) {
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b previewEntry) int { return a.number() - b.number() })
	if entries == nil {
		entries = []previewEntry{}
	}
	spec := f.doc["spec"].(map[string]any)
	spec["inputs"] = entries
	defer func() { spec["inputs"] = f.entries }()
	body, err := yaml.Marshal(f.doc)
	if err != nil {
		return nil, fmt.Errorf("flux-preview: inputs file: %w", err)
	}
	out := append([]byte(f.header), body...)
	back, err := parseInputs(out)
	if err != nil {
		return nil, fmt.Errorf("flux-preview: the written inputs file does not read back: %w", err)
	}
	if !slices.Equal(back.entries, entries) && (len(back.entries) != 0 || len(entries) != 0) {
		return nil, fmt.Errorf("flux-preview: the written inputs file reads back as %v", back.entries)
	}
	return out, nil
}
