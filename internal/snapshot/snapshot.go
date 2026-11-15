// Package snapshot resolves what a run will execute: the project's workflow
// definition and the roles it uses, read from the base branch head and frozen
// into the workflow input when the run starts (invariant 4, D39, D40).
package snapshot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"path"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
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
	// ProtectedPaths are the platform's protected-path patterns plus the
	// repo's own, read from the base branch so a change cannot loosen the
	// rules it is judged by (invariant 6).
	ProtectedPaths []string `json:"protectedPaths,omitempty"`
	// IssueBudgetUSD is the project's cap on what the issue spends on
	// models; zero means the platform's default (D53).
	IssueBudgetUSD float64 `json:"issueBudgetUSD,omitempty"`
	// EgressAllow are the hosts the project adds to the platform's egress
	// list (D80), as the base branch declared them when the run started.
	EgressAllow []string `json:"egressAllow,omitempty"`
	// MergeMethod is how the merge queue merges the run's change request
	// (D88); empty in snapshots taken before it, which never enqueue.
	MergeMethod domain.MergeMethod `json:"mergeMethod,omitempty"`
	// MergeApproval is whether a person approves the change request before
	// the merge queue takes it (D92): the project's setting, then the issue's
	// labels as the run started.
	MergeApproval bool `json:"mergeApproval,omitempty"`
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

// ErrInvalid wraps a configuration error the repo must fix; any other error is
// a failure to read and worth retrying.
var ErrInvalid = errors.New("invalid project configuration")

// errRead marks failures to read, so they are not mistaken for invalid content.
var errRead = errors.New("read")

// Resolve reads the project's `.factory/config.yaml`, protected paths,
// workflow definition and roles at the head of baseBranch. Repo definitions and roles take precedence
// over built-in ones of the same name; a project that selects no workflow runs
// the built-in default (workflows.Default).
func Resolve(ctx context.Context, r FileReader, repo, baseBranch string) (Snapshot, error) {
	return ResolveFor(ctx, r, repo, baseBranch, "")
}

// ResolveFor is Resolve for an issue of issueType: the project's definition
// for that type, else its definition, else the platform's for the type (D94).
func ResolveFor(ctx context.Context, r FileReader, repo, baseBranch, issueType string) (Snapshot, error) {
	rd, err := open(ctx, r, repo, baseBranch)
	if err != nil {
		return Snapshot{}, err
	}
	project, err := rd.project()
	if err != nil {
		return Snapshot{}, err
	}
	project.Workflow = cmp.Or(project.WorkflowFor(issueType), workflows.DefaultFor(issueType))

	s := Snapshot{BaseSHA: rd.sha, IssueBudgetUSD: project.Budgets.IssueUSD, EgressAllow: project.Egress.Allow, MergeMethod: project.MergeMethod(),
		MergeApproval: project.MergeApprovalRequired(nil)}
	raw, found, err := rd.read(config.ProtectedPathsFile)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: %w", err)
	}
	var repoProtected []string
	if found {
		if repoProtected, err = config.ParseProtectedPaths(raw); err != nil {
			return Snapshot{}, fmt.Errorf("snapshot: %w: %w", ErrInvalid, err)
		}
	}
	s.ProtectedPaths = config.ProtectedPatterns(repoProtected)
	defPath := path.Join(WorkflowsDir, project.Workflow+".yaml")
	raw, found, err = rd.read(defPath)
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
	if s.Roles, err = rolesResult(config.ResolveRoles(s.Workflow, rd.roleLookup())); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// reader reads a repo's files at the commit its base branch pointed to when the
// snapshot was taken, so everything in one snapshot comes from one commit.
type reader struct {
	ctx  context.Context
	r    FileReader
	repo string
	sha  string
}

func open(ctx context.Context, r FileReader, repo, baseBranch string) (*reader, error) {
	sha, err := r.ResolveRef(ctx, repo, baseBranch)
	if err != nil {
		return nil, fmt.Errorf("snapshot: resolve %s@%s: %w", repo, baseBranch, err)
	}
	return &reader{ctx: ctx, r: r, repo: repo, sha: sha}, nil
}

func (rd *reader) read(p string) ([]byte, bool, error) {
	b, found, err := rd.r.FileAt(rd.ctx, rd.repo, p, rd.sha)
	if err != nil {
		return nil, false, fmt.Errorf("%w %s@%s: %w", errRead, p, rd.sha, err)
	}
	return b, found, nil
}

// project reads `.factory/config.yaml`; a repo without one has the defaults.
func (rd *reader) project() (config.Project, error) {
	raw, found, err := rd.read(config.ProjectConfigPath)
	if err != nil {
		return config.Project{}, fmt.Errorf("snapshot: %w", err)
	}
	var project config.Project
	if found {
		// The services limit is the platform's and is checked per pod.
		if project, err = config.ParseProject(raw, math.MaxInt); err != nil {
			return config.Project{}, fmt.Errorf("snapshot: %w: %w", ErrInvalid, err)
		}
	}
	return project, nil
}

// roleLookup finds a role in the repo's .factory/roles/, else among the built-ins.
func (rd *reader) roleLookup() config.RoleLookup {
	return func(name string) (config.Role, error) {
		rolePath := path.Join(RolesDir, name+".yaml")
		raw, found, err := rd.read(rolePath)
		if err != nil {
			return config.Role{}, err
		}
		if !found {
			return roles.Get(name)
		}
		role, err := roles.Parse(raw, func(p string) ([]byte, error) {
			b, found, err := rd.read(path.Join(RolesDir, p))
			if err == nil && !found {
				err = fmt.Errorf("%s not found", path.Join(RolesDir, p))
			}
			return b, err
		})
		if err == nil && role.Name != name {
			err = fmt.Errorf("%s is named %q", rolePath, role.Name)
		}
		return role, err
	}
}

// rolesResult tells a failed read (worth retrying) from roles the repo must fix.
func rolesResult(rs map[string]config.Role, err error) (map[string]config.Role, error) {
	if errors.Is(err, errRead) {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w: %w", ErrInvalid, err)
	}
	return rs, nil
}
