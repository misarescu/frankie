package flux

import (
	"strings"
	"testing"
)

// The staging kustomization of Goodreads-CD, comments included.
const stagingKustomization = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: goodreads-stg
resources:
  - namespace.yaml
  - ../base
images:
  - name: ghcr.io/misarescu/other
    newTag: sha-0000000
  - name: ghcr.io/misarescu/goodreads-clone
    # The release this stage runs: sha-<short> of a Goodreads-Clone commit on main.
    newTag: sha-a44235f # set by Frankie
patches:
  - target:
      kind: Ingress
      name: goodreads
`

// M7, D117: the driver changes only the image's newTag.
func TestSetImageTag(t *testing.T) {
	got, err := setImageTag([]byte(stagingKustomization), "ghcr.io/misarescu/goodreads-clone", "sha-1234567")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(stagingKustomization, "newTag: sha-a44235f # set by Frankie", "newTag: sha-1234567 # set by Frankie", 1)
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if tag, err := imageTag(got, "ghcr.io/misarescu/other"); err != nil || tag != "sha-0000000" {
		t.Errorf("other image = %q, %v", tag, err)
	}

	quoted := "images:\n- name: \"app\"\n  newTag: \"v1\"\n"
	if got, err := setImageTag([]byte(quoted), "app", "v2"); err != nil || string(got) != "images:\n- name: \"app\"\n  newTag: \"v2\"\n" {
		t.Errorf("quoted = %q, %v", got, err)
	}
}

func TestSetImageTagRejects(t *testing.T) {
	for name, tt := range map[string]struct{ content, image, tag string }{
		"unknown image": {stagingKustomization, "ghcr.io/x/y", "v2"},
		"bad tag":       {stagingKustomization, "ghcr.io/misarescu/goodreads-clone", "v2 # x"},
		"empty tag":     {stagingKustomization, "ghcr.io/misarescu/goodreads-clone", ""},
		"no newTag":     {"images:\n  - name: app\n    newName: x\n", "app", "v2"},
		"flow style":    {"images: [{name: app, newTag: v1}]\n", "app", "v2"},
		"not yaml":      {"images: [", "app", "v2"},
	} {
		if _, err := setImageTag([]byte(tt.content), tt.image, tt.tag); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := imageTag([]byte("images:\n  - name: app\n"), "app"); err == nil {
		t.Error("an entry without newTag read as a tag")
	}
}
