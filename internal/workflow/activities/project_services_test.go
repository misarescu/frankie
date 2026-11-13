package activities

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestProjectServices(t *testing.T) {
	limits := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("512Mi")}
	tests := []struct {
		name        string
		files       map[string]string
		max         int
		wantNames   []string
		wantInvalid bool
	}{
		{"no project config", map[string]string{}, 1, nil, false},
		{"postgres from the catalog", map[string]string{".factory/config.yaml": "services: [postgres:17]\n"}, 1, []string{"postgres"}, false},
		{"over the platform limit", map[string]string{".factory/config.yaml": "services: [postgres:17]\n"}, 0, nil, true},
		{"malformed", map[string]string{".factory/config.yaml": "services: {postgres: 17}\n"}, 1, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Activities{GitHub: fakeRepo(t, tt.files), Services: ServicesConfig{MaxPerPod: tt.max, Limits: limits}}
			got, err := a.projectServices(t.Context(), "misarescu/Goodreads-Clone", pushedSHA)
			if tt.wantInvalid {
				if !errors.Is(err, errInvalidProjectConfig) {
					t.Fatalf("err = %v, want errInvalidProjectConfig", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.wantNames) {
				t.Fatalf("services = %+v", got)
			}
			for i, s := range got {
				if s.Name != tt.wantNames[i] || s.Limits.Memory().String() != "512Mi" || s.RunAsUser == nil {
					t.Errorf("service %d = %+v", i, s)
				}
			}
		})
	}
}

func TestProjectServicesReadErrorIsNotInvalidConfig(t *testing.T) {
	a := &Activities{GitHub: fakeRepo(t, map[string]string{}), Services: ServicesConfig{MaxPerPod: 1}}
	// An unknown ref makes the fake GitHub answer 400: a read failure, retryable.
	_, err := a.projectServices(t.Context(), "misarescu/Goodreads-Clone", "not-a-commit")
	if err == nil || errors.Is(err, errInvalidProjectConfig) {
		t.Fatalf("err = %v; want a read error, not an invalid config", err)
	}
}
