package activities

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/sandbox"
)

// ServicesConfig is the platform's policy for sidecar services (FR-SB-9); a
// repo chooses which services, the platform chooses how big and how many.
type ServicesConfig struct {
	MaxPerPod int                 // 0 refuses any declared service
	Limits    corev1.ResourceList // per service, taken out of the pod's own limits
}

// errInvalidProjectConfig marks a `.factory/config.yaml` the repo got wrong, as
// opposed to failing to read it.
var errInvalidProjectConfig = errors.New("invalid project config")

// projectConfig reads the project config repo declares at ref (a branch or
// SHA); a repo without one has the zero config.
func (a *Activities) projectConfig(ctx context.Context, repo, ref string) (config.Project, error) {
	raw, found, err := a.GitHub.FileAt(ctx, repo, config.ProjectConfigPath, ref)
	if err != nil {
		return config.Project{}, fmt.Errorf("read %s: %w", config.ProjectConfigPath, err)
	}
	if !found {
		return config.Project{}, nil
	}
	p, err := config.ParseProject(raw, a.Services.MaxPerPod)
	if err != nil {
		return config.Project{}, fmt.Errorf("%w: %w", errInvalidProjectConfig, err)
	}
	return p, nil
}

// projectServices returns the services repo declares at ref (a branch or SHA).
func (a *Activities) projectServices(ctx context.Context, repo, ref string) ([]sandbox.Service, error) {
	p, err := a.projectConfig(ctx, repo, ref)
	if err != nil {
		return nil, err
	}
	out := make([]sandbox.Service, 0, len(p.Services))
	for _, s := range p.Services {
		out = append(out, sandbox.Service{Name: s.Name, Image: s.Image, Port: s.Port, Env: s.Env,
			RunAsUser: s.RunAsUser, DataDir: s.DataDir, Limits: a.Services.Limits.DeepCopy()})
	}
	return out, nil
}
