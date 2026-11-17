package flux

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// kustomizationImages is the part of a kustomization.yaml the driver reads.
type kustomizationImages struct {
	Images []struct {
		Name   string `json:"name"`
		NewTag string `json:"newTag"`
	} `json:"images"`
}

// imageTag returns the newTag the kustomization pins image to.
func imageTag(content []byte, image string) (string, error) {
	var k kustomizationImages
	if err := yaml.Unmarshal(content, &k); err != nil {
		return "", fmt.Errorf("flux: kustomization: %w", err)
	}
	for _, i := range k.Images {
		if i.Name == image {
			if i.NewTag == "" {
				return "", fmt.Errorf("flux: kustomization pins %s without a newTag", image)
			}
			return i.NewTag, nil
		}
	}
	return "", fmt.Errorf("flux: kustomization has no images entry for %s", image)
}

var (
	itemRE   = regexp.MustCompile(`^(\s*)-\s+name:\s*["']?([^"'#\s]+)["']?\s*(#.*)?$`)
	newTagRE = regexp.MustCompile(`^(\s*newTag:\s*)(["']?)[^"'#\s]*(["']?)(\s*(#.*)?)$`)
)

// setImageTag changes the newTag of image's entry to tag, line by line, so
// comments and layout stay as they are; the result is read back to check that
// exactly that changed.
func setImageTag(content []byte, image, tag string) ([]byte, error) {
	if strings.ContainsAny(tag, "\"'#: \n") || tag == "" {
		return nil, fmt.Errorf("flux: %q is not an image tag", tag)
	}
	lines := strings.Split(string(content), "\n")
	inEntry, itemIndent, changed := false, "", 0
	for i, l := range lines {
		if m := itemRE.FindStringSubmatch(l); m != nil {
			inEntry, itemIndent = m[2] == image, m[1]
			continue
		}
		if inEntry && strings.TrimSpace(l) != "" && !strings.HasPrefix(l, itemIndent+"  ") {
			inEntry = false // the entry ended
		}
		if inEntry {
			if m := newTagRE.FindStringSubmatch(l); m != nil {
				lines[i] = m[1] + m[2] + tag + m[3] + m[4]
				changed++
			}
		}
	}
	if changed != 1 {
		return nil, errors.New("flux: found no single newTag line for " + image + " to change")
	}
	out := []byte(strings.Join(lines, "\n"))
	got, err := imageTag(out, image)
	if err != nil {
		return nil, fmt.Errorf("flux: changing the newTag of %s broke the kustomization: %w", image, err)
	}
	if got != tag {
		return nil, fmt.Errorf("flux: changing the newTag of %s did not take (read back %q)", image, got)
	}
	return out, nil
}
