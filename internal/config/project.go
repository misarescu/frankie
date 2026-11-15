// Package config parses and validates configuration that lives in project
// repos (docs/spec/02: `.factory/config.yaml` is authoritative for a project).
// M2 reads the services section, M3 the workflow, M3.1 the dependencies; the
// rest of the project config arrives with the milestones that use it.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/yaml"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/egress"
)

// ProjectConfigPath is where a repo declares its project config.
const ProjectConfigPath = ".factory/config.yaml"

// Project is the parsed `.factory/config.yaml`.
type Project struct {
	// Workflow names the workflow definition the project's issues run: a
	// built-in one (workflows/) or `.factory/workflows/<name>.yaml` (D39).
	Workflow string `json:"workflow,omitempty"`
	// Services run next to agent steps and command gates for integration tests
	// (FR-SB-9), reachable on localhost.
	Services []Service `json:"services,omitempty"`
	// Dependencies are baked into a dependency image on top of the toolchain
	// image (M3.1, docs/spec/05), so steps do not download and compile them
	// on every run.
	Dependencies *Dependencies `json:"dependencies,omitempty"`
	// Budgets cap model spend (D53).
	Budgets Budgets `json:"budgets,omitempty"`
	// Egress adds hosts to the platform's egress list for the project's agent,
	// gate and build pods (D80).
	Egress Egress `json:"egress,omitempty"`
	// SpecApproval is "required" when an issue's spec needs a person's approval
	// before it runs, or "off" (the default, also when empty) (D64). Issue labels
	// override it per issue: LabelNeedsSpecApproval and LabelNoSpecApproval.
	SpecApproval string `json:"specApproval,omitempty"`
	// Merge says how the merge queue lands the project's change requests (D88).
	Merge Merge `json:"merge,omitempty"`
	// MergeApproval is "required" when a person approves each change request
	// before the merge queue takes it, or "off" (the default, also when empty)
	// (D92). Issue labels override it: LabelNeedsMergeApproval and
	// LabelNoMergeApproval.
	MergeApproval string `json:"mergeApproval,omitempty"`
}

// Merge approval labels (D92).
const (
	LabelNeedsMergeApproval = "needs-merge-approval"
	LabelNoMergeApproval    = "no-merge-approval"
)

// MergeApprovalRequired reports whether an issue with labels needs a person
// to approve its change request before it merges: a label decides, else the
// project.
func (p Project) MergeApprovalRequired(labels []string) bool {
	return MergeApprovalWithLabels(p.MergeApproval == SpecApprovalRequired, labels)
}

// MergeApprovalWithLabels applies an issue's labels to its project's merge
// approval setting.
func MergeApprovalWithLabels(project bool, labels []string) bool {
	switch {
	case slices.Contains(labels, LabelNoMergeApproval):
		return false
	case slices.Contains(labels, LabelNeedsMergeApproval):
		return true
	}
	return project
}

// Merge is how the merge queue merges a project's change requests.
type Merge struct {
	// Method is squash (the default, also when empty), rebase or merge.
	Method domain.MergeMethod `json:"method,omitempty"`
}

// MergeMethod is the project's merge method, squash unless it chose another.
func (p Project) MergeMethod() domain.MergeMethod {
	if p.Merge.Method == "" {
		return domain.MergeSquash
	}
	return p.Merge.Method
}

// Spec approval settings and the issue labels that override them (D64).
const (
	SpecApprovalRequired   = "required"
	SpecApprovalOff        = "off"
	LabelNeedsSpecApproval = "needs-spec-approval"
	LabelNoSpecApproval    = "no-spec-approval"
)

// RequiresSpecApproval reports whether the project's issues need an approved spec
// unless a label says otherwise.
func (p Project) RequiresSpecApproval() bool { return p.SpecApproval == SpecApprovalRequired }

// Egress is what a project adds to the platform's egress list (D80). It is
// read from the base branch when a run starts, so a change to it takes effect
// for runs started after it merges; `.factory/**` is a protected path.
type Egress struct {
	// Allow lists plain hostnames the project's pods may reach on port 443.
	Allow []string `json:"allow,omitempty"`
}

// Budgets cap what a project's work may spend on models, in USD, enforced
// by the LLM gateway through each session's virtual key (D53). Zero means
// the platform's default.
type Budgets struct {
	// IssueUSD caps all runs of one issue together.
	IssueUSD float64 `json:"issueUSD,omitempty"`
}

// MaxIssueBudgetUSD bounds a project's issue budget.
const MaxIssueBudgetUSD = 1000

// Dependencies declares how to warm a repo's dependencies, for any ecosystem:
// which files the warm-up reads, and the command that downloads, and where it
// can, pre-compiles them. The platform knows nothing per language.
type Dependencies struct {
	// Files are globs from the repo root (as in protected paths: `**` spans
	// directories). mise.toml and mise.lock are always added, so the
	// toolchain's tools and tasks resolve in the warm-up.
	Files []string `json:"files"`
	// Run is a shell command run from the directory holding the files, as the
	// agent user with the sandbox environment.
	Run string `json:"run"`
}

// Matches reports whether the repo path p is one of the declared files.
func (d Dependencies) Matches(p string) bool {
	_, ok := MatchProtected(d.Files, p)
	return ok
}

const (
	maxDependencyPatterns = 50
	maxDependencyRun      = 2000
)

func (d Dependencies) validate() []error {
	var errs []error
	switch {
	case len(d.Files) == 0:
		errs = append(errs, errors.New("dependencies.files: at least one file is required"))
	case len(d.Files) > maxDependencyPatterns:
		errs = append(errs, fmt.Errorf("dependencies.files: %d patterns, at most %d", len(d.Files), maxDependencyPatterns))
	}
	for i, f := range d.Files {
		if err := validGlob(f); err != nil {
			errs = append(errs, fmt.Errorf("dependencies.files[%d]: %w", i, err))
		}
	}
	switch {
	case strings.TrimSpace(d.Run) == "":
		errs = append(errs, errors.New("dependencies.run: is required"))
	case len(d.Run) > maxDependencyRun || strings.ContainsRune(d.Run, 0):
		errs = append(errs, fmt.Errorf("dependencies.run: must be at most %d bytes of text", maxDependencyRun))
	}
	return errs
}

// Service is one sidecar service. In the file it is either an image reference
// ("postgres:17") or an object; well-known images get defaults from the catalog.
type Service struct {
	Name  string            `json:"name"`
	Image string            `json:"image"`
	Port  int               `json:"port,omitempty"` // checked before the step's commands start
	Env   map[string]string `json:"env,omitempty"`

	// From the catalog only, never from the repo.
	RunAsUser *int64 `json:"-"` // the image's own non-root user
	DataDir   string `json:"-"` // writable directory the image needs (an emptyDir)
}

// UnmarshalJSON accepts the "image:tag" shorthand or a strict object.
func (s *Service) UnmarshalJSON(b []byte) error {
	var short string
	if err := json.Unmarshal(b, &short); err == nil {
		*s = Service{Image: short}
		return nil
	}
	type plain Service
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var p plain
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	*s = Service(p)
	return nil
}

// catalogEntry fills in what a well-known image needs to run in a sandbox:
// restricted Pod Security (non-root), no setup by hand, a port to wait for.
type catalogEntry struct {
	port      int
	env       map[string]string
	runAsUser int64
	dataDir   string
}

// catalog is keyed by the image's repository name without registry or tag.
var catalog = map[string]catalogEntry{
	// Trust auth on localhost only: the sidecar is reachable only inside the pod.
	"postgres": {port: 5432, runAsUser: 999, dataDir: "/var/lib/postgresql/data",
		env: map[string]string{"POSTGRES_HOST_AUTH_METHOD": "trust", "PGDATA": "/var/lib/postgresql/data/pgdata"}},
	"redis": {port: 6379, runAsUser: 999, dataDir: "/data"},
}

var (
	imageRE  = regexp.MustCompile(`^[a-z0-9]+([._/:@-][a-zA-Z0-9_.-]+)*$`)
	envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// repositoryName is "postgres" for "docker.io/library/postgres:17".
func repositoryName(image string) string {
	ref := image
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref[strings.LastIndex(ref, "/")+1:]
}

// ParseProject parses and validates `.factory/config.yaml`. maxServices is the
// platform's limit per pod (profile configuration, docs/spec/09).
func ParseProject(raw []byte, maxServices int) (Project, error) {
	var p Project
	if err := yaml.UnmarshalStrict(raw, &p); err != nil {
		return Project{}, fmt.Errorf("%s: %w", ProjectConfigPath, err)
	}
	if len(p.Services) > maxServices {
		return Project{}, fmt.Errorf("%s: %d services declared, the platform allows %d per pod", ProjectConfigPath, len(p.Services), maxServices)
	}
	var errs []error
	if p.Workflow != "" && (!identifierRE.MatchString(p.Workflow) || len(p.Workflow) > maxIdentifierLength) {
		errs = append(errs, fmt.Errorf("workflow: %q must be lowercase letters, digits and dashes, at most %d characters", p.Workflow, maxIdentifierLength))
	}
	if p.Dependencies != nil {
		errs = append(errs, p.Dependencies.validate()...)
	}
	// YAML reads an unquoted off as false, so the boolean forms are accepted too.
	switch p.SpecApproval {
	case "true":
		p.SpecApproval = SpecApprovalRequired
	case "false":
		p.SpecApproval = SpecApprovalOff
	}
	if p.SpecApproval != "" && p.SpecApproval != SpecApprovalRequired && p.SpecApproval != SpecApprovalOff {
		errs = append(errs, fmt.Errorf("specApproval: %q must be %q or %q", p.SpecApproval, SpecApprovalRequired, SpecApprovalOff))
	}
	switch p.MergeApproval {
	case "true":
		p.MergeApproval = SpecApprovalRequired
	case "false":
		p.MergeApproval = SpecApprovalOff
	}
	if p.MergeApproval != "" && p.MergeApproval != SpecApprovalRequired && p.MergeApproval != SpecApprovalOff {
		errs = append(errs, fmt.Errorf("mergeApproval: %q must be %q or %q", p.MergeApproval, SpecApprovalRequired, SpecApprovalOff))
	}
	if m := p.Merge.Method; m != "" {
		if err := m.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("merge.method: %w", err))
		}
	}
	if b := p.Budgets.IssueUSD; b < 0 || b > MaxIssueBudgetUSD {
		errs = append(errs, fmt.Errorf("budgets.issueUSD: %v must be between 0 (the platform's default) and %d", b, MaxIssueBudgetUSD))
	}
	if err := egress.ValidateProjectHosts(p.Egress.Allow); err != nil {
		errs = append(errs, err)
	}
	seen := map[string]bool{}
	for i := range p.Services {
		s := &p.Services[i]
		if !imageRE.MatchString(s.Image) {
			errs = append(errs, fmt.Errorf("services[%d]: image %q is not an image reference", i, s.Image))
			continue
		}
		repo := repositoryName(s.Image)
		if s.Name == "" {
			s.Name = repo
		}
		if c, ok := catalog[repo]; ok {
			if s.Port == 0 {
				s.Port = c.port
			}
			env := maps.Clone(c.env)
			if env == nil {
				env = map[string]string{}
			}
			maps.Copy(env, s.Env) // the repo's values win
			s.Env, s.RunAsUser, s.DataDir = env, &c.runAsUser, c.dataDir
		}
		if msgs := validation.IsDNS1123Label(s.Name); len(msgs) > 0 || len(s.Name) > 40 {
			errs = append(errs, fmt.Errorf("services[%d]: name %q must be a DNS label of at most 40 characters", i, s.Name))
		}
		if seen[s.Name] {
			errs = append(errs, fmt.Errorf("services[%d]: duplicate name %q", i, s.Name))
		}
		seen[s.Name] = true
		if s.Port < 0 || s.Port > 65535 {
			errs = append(errs, fmt.Errorf("services[%d]: port %d out of range", i, s.Port))
		}
		for _, k := range slices.Sorted(maps.Keys(s.Env)) {
			if !envKeyRE.MatchString(k) {
				errs = append(errs, fmt.Errorf("services[%d]: env name %q is invalid", i, k))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return Project{}, fmt.Errorf("%s: %w", ProjectConfigPath, err)
	}
	return p, nil
}
