// Package snapshot resolves what a run will execute: the project's workflow
// definition and the roles it uses, read from the base branch head and frozen
// into the workflow input when the run starts (invariant 4, D39, D40).
package snapshot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/roles"
	"github.com/misarescu/frankie/workflows"
)

// Paths in a project repo.
const (
	WorkflowsDir = ".factory/workflows"
	RolesDir     = ".factory/roles"
)

// Snapshot is everything a run executes, fixed when it starts. Its JSON form is
// part of recorded workflow histories and of runs.workflow_snapshot.
type Snapshot struct {
	Workflow config.Workflow        `json:"workflow"`
	Roles    map[string]config.Role `json:"roles"`
	// Source says where the definition came from: "built-in" or the repo path.
	Source string `json:"source"`
	// BaseSHA is the base branch commit the definition and roles were read at.
	BaseSHA string `json:"baseSHA"`
	// Error is set instead of the rest when the project's configuration is
	// invalid; the run then fails with it, visibly, rather than not starting.
	Error string `json:"error,omitempty"`
}

// Version is the run's workflow version as recorded on the Run: name@version.
func (s Snapshot) Version() string {
	if s.Error != "" {
		return "invalid"
	}
	return fmt.Sprintf("%s@%d", s.Workflow.Name, s.Workflow.Version)
}

// FileReader reads a repo's files at a commit; GitProvider implements it.
type FileReader interface {
	ResolveRef(ctx context.Context, repo, ref string) (string, error)
	FileAt(ctx context.Context, repo, path, ref string) (content []byte, found bool, err error)
}

var (
	// ErrNoWorkflow means the project selects no workflow. Until the interpreter
	// runs the built-in default (M3), such projects keep the M2 IssueRun.
	ErrNoWorkflow = errors.New("the project selects no workflow")
	// ErrInvalid wraps a configuration error the repo must fix; any other error
	// is a failure to read and worth retrying.
	ErrInvalid = errors.New("invalid project configuration")
)

// errRead marks failures to read, so they are not mistaken for invalid content.
var errRead = errors.New("read")

// Resolve reads the project's `.factory/config.yaml`, workflow definition and
// roles at the head of baseBranch. Repo definitions and roles take precedence
// over built-in ones of the same name.
func Resolve(ctx context.Context, r FileReader, repo, baseBranch string) (Snapshot, error) {
	sha, err := r.ResolveRef(ctx, repo, baseBranch)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: resolve %s@%s: %w", repo, baseBranch, err)
	}
	read := func(p string) ([]byte, bool, error) {
		b, found, err := r.FileAt(ctx, repo, p, sha)
		if err != nil {
			return nil, false, fmt.Errorf("%w %s@%s: %w", errRead, p, sha, err)
		}
		return b, found, nil
	}

	raw, found, err := read(config.ProjectConfigPath)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: %w", err)
	}
	if !found {
		return Snapshot{}, ErrNoWorkflow
	}
	// The services limit is the platform's and is checked per pod.
	project, err := config.ParseProject(raw, math.MaxInt)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: %w: %w", ErrInvalid, err)
	}
	if project.Workflow == "" {
		return Snapshot{}, ErrNoWorkflow
	}

	s := Snapshot{BaseSHA: sha}
	defPath := path.Join(WorkflowsDir, project.Workflow+".yaml")
	raw, found, err = read(defPath)
	switch {
	case err != nil:
		return Snapshot{}, fmt.Errorf("snapshot: %w", err)
	case found:
		s.Source = defPath
		if s.Workflow, err = config.ParseWorkflow(raw); err != nil {
			return Snapshot{}, fmt.Errorf("snapshot: %w: %s: %w", ErrInvalid, defPath, err)
		}
		if s.Workflow.Name != project.Workflow {
			return Snapshot{}, fmt.Errorf("snapshot: %w: %s is named %q", ErrInvalid, defPath, s.Workflow.Name)
		}
	default:
		s.Source = "built-in"
		if s.Workflow, err = workflows.Get(project.Workflow); err != nil {
			return Snapshot{}, fmt.Errorf("snapshot: %w: workflow %q is neither in %s nor built in: %w", ErrInvalid, project.Workflow, WorkflowsDir, err)
		}
	}

	s.Roles, err = config.ResolveRoles(s.Workflow, func(name string) (config.Role, error) {
		rolePath := path.Join(RolesDir, name+".yaml")
		raw, found, err := read(rolePath)
		if err != nil {
			return config.Role{}, err
		}
		if !found {
			return roles.Get(name)
		}
		role, err := roles.Parse(raw, func(p string) ([]byte, error) {
			b, found, err := read(path.Join(RolesDir, p))
			if err == nil && !found {
				err = fmt.Errorf("%s not found", path.Join(RolesDir, p))
			}
			return b, err
		})
		if err == nil && role.Name != name {
			err = fmt.Errorf("%s is named %q", rolePath, role.Name)
		}
		return role, err
	})
	if errors.Is(err, errRead) {
		return Snapshot{}, fmt.Errorf("snapshot: %w", err)
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: %w: %w", ErrInvalid, err)
	}
	return s, nil
}
