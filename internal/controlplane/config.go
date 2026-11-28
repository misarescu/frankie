package controlplane

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/misarescu/frankie/internal/auth"
	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/images"
	"github.com/misarescu/frankie/internal/sandbox"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// RepoConfig is a project repo the factory serves and takes issues from.
type RepoConfig struct {
	Name       string `json:"name"`       // factory repo name; the GitHub owner/name in M1
	Upstream   string `json:"upstream"`   // git URL the git proxy forwards to
	KeyPrefix  string `json:"keyPrefix"`  // issue key prefix (D22)
	BaseBranch string `json:"baseBranch"` // default "main"
}

// Models are what the LLM gateway serves (docs/spec/04).
type Models struct {
	Providers config.Providers
	// Default is the model ref of steps whose definition and role name none.
	Default string
}

// fileConfig is the non-secret configuration, rendered by the Helm chart.
type fileConfig struct {
	Repos []RepoConfig `json:"repos"`
	// Providers are the LLM gateway's provider profiles (llmGateway.providers).
	Providers    config.Providers `json:"providers"`
	IntakeLabel  string           `json:"intakeLabel"`
	PollInterval string           `json:"pollInterval"`
	// MaxConcurrentIssues is scheduler.maxConcurrentIssues (D62); 0 means 1.
	MaxConcurrentIssues int `json:"maxConcurrentIssues"`
	Temporal            struct {
		Address   string `json:"address"`
		Namespace string `json:"namespace"`
	} `json:"temporal"`
	GatewayAdminURL string `json:"gatewayAdminURL"`
	Agent           struct {
		Namespace      string   `json:"namespace"`
		RunnerImage    string   `json:"runnerImage"`
		RuntimeClass   string   `json:"runtimeClass"`
		ServiceAccount string   `json:"serviceAccount"`
		EgressProxyURL string   `json:"egressProxyURL"`
		NoProxy        []string `json:"noProxy"`
		CPU            string   `json:"cpu"`
		Memory         string   `json:"memory"`
		CPURequest     string   `json:"cpuRequest"`         // D77; empty means the CPU limit
		ReviewerCPU    string   `json:"reviewerCPURequest"` // read-only steps; empty means cpuRequest
		ReviewerMemory string   `json:"reviewerMemory"`     // read-only steps' memory limit (D104); empty means memory
		WorkspaceSize  string   `json:"workspaceSize"`
		PodDeadline    string   `json:"podDeadline"`
		GitProxyURL    string   `json:"gitProxyURL"`
		GatewayURL     string   `json:"gatewayURL"`
		Model          string   `json:"model"` // model ref, served by Providers
		BudgetUSD      float64  `json:"budgetUSD"`
		IssueBudgetUSD float64  `json:"issueBudgetUSD"`
		SessionTimeout string   `json:"sessionTimeout"`
		IdleTimeout    string   `json:"idleTimeout"`
		AuthorName     string   `json:"authorName"`
		AuthorEmail    string   `json:"authorEmail"`
	} `json:"agent"`
	// Gate configures command gates (docs/spec/03).
	Gate struct {
		Commands      []string `json:"commands"`
		CPU           string   `json:"cpu"`
		Memory        string   `json:"memory"`
		CPURequest    string   `json:"cpuRequest"` // D77; empty means the CPU limit
		WorkspaceSize string   `json:"workspaceSize"`
		Deadline      string   `json:"deadline"`
	} `json:"gate"`
	// SandboxEnv is set in every agent, gate and toolchain build pod, e.g.
	// GOPROXY for the in-cluster Go module proxy (D37).
	SandboxEnv map[string]string `json:"sandboxEnv"`
	// SandboxNodeSelector places every agent, gate, branch update and build pod,
	// e.g. on an autoscaled agent pool apart from the platform's (D100).
	SandboxNodeSelector map[string]string `json:"sandboxNodeSelector"`
	// SandboxTolerations go on the same pods, so they can run on an agent pool
	// tainted to keep every other pod off it (D101).
	SandboxTolerations []corev1.Toleration `json:"sandboxTolerations"`
	// SandboxGuestMemoryHeadroom is VM memory beyond each sandbox pod's
	// container limits, for the guest kernel and agent (D103), e.g. "512Mi";
	// empty adds none.
	SandboxGuestMemoryHeadroom string `json:"sandboxGuestMemoryHeadroom"`
	// Services limits sidecar services per pod (FR-SB-9).
	Services struct {
		MaxPerPod int    `json:"maxPerPod"`
		CPU       string `json:"cpu"`
		Memory    string `json:"memory"`
	} `json:"services"`
	// Toolchains configures toolchain images (D30); an empty registryURL disables them.
	Toolchains struct {
		RegistryURL    string   `json:"registryURL"` // as the control plane reaches the registry
		PushHost       string   `json:"pushHost"`    // as build pods reach it
		PullHost       string   `json:"pullHost"`    // as nodes reach it
		Namespace      string   `json:"namespace"`
		RuntimeClass   string   `json:"runtimeClass"`
		ServiceAccount string   `json:"serviceAccount"`
		EgressProxyURL string   `json:"egressProxyURL"`
		NoProxy        []string `json:"noProxy"`
		CPU            string   `json:"cpu"`
		Memory         string   `json:"memory"`
		CPURequest     string   `json:"cpuRequest"` // D77; empty means the CPU limit
		ScratchSize    string   `json:"scratchSize"`
		Deadline       string   `json:"deadline"`
		// Collect configures registry collection (D84); an empty interval disables it.
		Collect struct {
			Interval           string `json:"interval"`
			MinAge             string `json:"minAge"`
			Keep               int    `json:"keep"`
			RegistryNamespace  string `json:"registryNamespace"`
			RegistryDeployment string `json:"registryDeployment"`
		} `json:"collect"`
	} `json:"toolchains"`
	// EgressProjectAllow is egressProxy.projectAllow (D80).
	EgressProjectAllow bool `json:"egressProjectAllow"`
	// Auth configures M10 sign-in, sessions, allowlist and SSO (D144-D147).
	Auth struct {
		PublicURL      string   `json:"publicURL"`
		AllowedOrigins []string `json:"allowedOrigins"`
		SessionTTL     string   `json:"sessionTTL"`
		SecureCookie   *bool    `json:"secureCookie"`
		PreviewMode    bool     `json:"previewMode"`
		Allowlist      struct {
			EmailDomains []string `json:"emailDomains"`
			GitHubOrgs   []string `json:"githubOrgs"`
			DefaultRole  string   `json:"defaultRole"`
		} `json:"allowlist"`
		SSO struct {
			Google struct {
				Enabled   bool   `json:"enabled"`
				IssuerURL string `json:"issuerURL"`
			} `json:"google"`
			GitLab struct {
				Enabled   bool   `json:"enabled"`
				IssuerURL string `json:"issuerURL"`
			} `json:"gitlab"`
			GitHub struct {
				Enabled bool `json:"enabled"`
			} `json:"github"`
		} `json:"sso"`
	} `json:"auth"`
}

// gate parses the command gate settings; without them gates use the agent's
// limits and workspace size, a 30-minute deadline and the default commands.
func (fc fileConfig) gate() (activities.GateConfig, error) {
	g := fc.Gate
	out := activities.GateConfig{Commands: g.Commands, Deadline: 30 * time.Minute}
	for i, c := range g.Commands {
		if strings.TrimSpace(c) == "" {
			return out, fmt.Errorf("controlplane: config: gate command %d is empty", i)
		}
	}
	if g.Deadline != "" {
		d, err := time.ParseDuration(g.Deadline)
		if err != nil || d <= 0 {
			return out, fmt.Errorf("controlplane: config: gate deadline %q invalid", g.Deadline)
		}
		out.Deadline = d
	}
	cpu, mem, ws := firstNonEmpty(g.CPU, fc.Agent.CPU), firstNonEmpty(g.Memory, fc.Agent.Memory), firstNonEmpty(g.WorkspaceSize, fc.Agent.WorkspaceSize)
	if cpu != "" || mem != "" {
		c, err1 := resource.ParseQuantity(cpu)
		m, err2 := resource.ParseQuantity(mem)
		if err1 != nil || err2 != nil {
			return out, fmt.Errorf("controlplane: config: gate cpu %q / memory %q invalid", cpu, mem)
		}
		out.Limits = corev1.ResourceList{corev1.ResourceCPU: c, corev1.ResourceMemory: m}
	}
	if ws != "" {
		q, err := resource.ParseQuantity(ws)
		if err != nil {
			return out, fmt.Errorf("controlplane: config: gate workspace size %q: %w", ws, err)
		}
		out.WorkspaceSize = q
	}
	var err error
	if out.CPURequest, err = cpuRequest("gate", g.CPURequest, out.Limits); err != nil {
		return out, err
	}
	return out, nil
}

// guestMemoryHeadroom parses the sandbox VMs' memory headroom (D103): empty
// means none; otherwise a quantity that is not negative.
func guestMemoryHeadroom(raw string) (resource.Quantity, error) {
	if raw == "" {
		return resource.Quantity{}, nil
	}
	q, err := resource.ParseQuantity(raw)
	if err == nil {
		err = sandbox.ValidateGuestMemoryHeadroom(q)
	}
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("controlplane: config: sandboxGuestMemoryHeadroom %q invalid: %w", raw, err)
	}
	return q, nil
}

// cpuRequest parses what a pod type requests of its CPU limit (D77): empty
// means the limit; otherwise it must be positive and at most the limit.
func cpuRequest(what, raw string, limits corev1.ResourceList) (resource.Quantity, error) {
	if raw == "" {
		return resource.Quantity{}, nil
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil || q.Sign() <= 0 {
		return resource.Quantity{}, fmt.Errorf("controlplane: config: %s cpuRequest %q invalid", what, raw)
	}
	lim, ok := limits[corev1.ResourceCPU]
	if !ok {
		return resource.Quantity{}, fmt.Errorf("controlplane: config: %s cpuRequest needs a cpu limit", what)
	}
	if q.Cmp(lim) > 0 {
		return resource.Quantity{}, fmt.Errorf("controlplane: config: %s cpuRequest %s exceeds its cpu limit %s", what, q.String(), lim.String())
	}
	return q, nil
}

// services parses the sidecar policy; without it no service may be declared.
func (fc fileConfig) services() (activities.ServicesConfig, error) {
	sv := fc.Services
	if sv.MaxPerPod < 0 {
		return activities.ServicesConfig{}, fmt.Errorf("controlplane: config: services.maxPerPod %d < 0", sv.MaxPerPod)
	}
	if sv.MaxPerPod == 0 {
		return activities.ServicesConfig{}, nil
	}
	cpu, err1 := resource.ParseQuantity(sv.CPU)
	mem, err2 := resource.ParseQuantity(sv.Memory)
	if err1 != nil || err2 != nil {
		return activities.ServicesConfig{}, fmt.Errorf("controlplane: config: services cpu %q / memory %q invalid", sv.CPU, sv.Memory)
	}
	return activities.ServicesConfig{MaxPerPod: sv.MaxPerPod,
		Limits: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem}}, nil
}

var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateSandboxEnv refuses names that are not environment variables and the
// proxy settings, which only the sandbox builders set (D20).
func validateSandboxEnv(env map[string]string) error {
	for k := range env {
		if !envNameRE.MatchString(k) {
			return fmt.Errorf("controlplane: config: sandboxEnv name %q is invalid", k)
		}
		switch strings.ToUpper(k) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY":
			return fmt.Errorf("controlplane: config: sandboxEnv may not set %s; the egress proxy settings are fixed", k)
		}
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ToolchainConfig is how toolchain images are found and built; the zero value
// disables them.
type ToolchainConfig struct {
	RegistryURL string // control plane -> registry, e.g. http://frankie-registry.frankie.svc:5000
	PullHost    string // node -> registry, e.g. localhost:30500
	Build       images.BuildConfig
	Collect     CollectConfig
}

// CollectConfig is when registry collection runs and what it keeps (D84); a
// zero Interval disables it.
type CollectConfig struct {
	Interval, MinAge   time.Duration
	Keep               int
	RegistryNamespace  string
	RegistryDeployment string
}

// minCollectInterval keeps collections, which restart the registry, rare.
const minCollectInterval = time.Hour

func (fc fileConfig) collect() (CollectConfig, error) {
	c := fc.Toolchains.Collect
	if c.Interval == "" {
		return CollectConfig{}, nil
	}
	interval, err1 := time.ParseDuration(c.Interval)
	minAge, err2 := time.ParseDuration(cmp.Or(c.MinAge, "0s"))
	if err := errors.Join(err1, err2); err != nil {
		return CollectConfig{}, fmt.Errorf("controlplane: config: toolchains.collect interval %q or minAge %q invalid: %w", c.Interval, c.MinAge, err)
	}
	switch {
	case interval < minCollectInterval:
		return CollectConfig{}, fmt.Errorf("controlplane: config: toolchains.collect.interval %s is under %s", interval, minCollectInterval)
	case minAge < 0 || c.Keep < 1:
		return CollectConfig{}, fmt.Errorf("controlplane: config: toolchains.collect needs keep >= 1 (got %d) and minAge >= 0 (got %s)", c.Keep, minAge)
	case c.RegistryNamespace == "" || c.RegistryDeployment == "":
		return CollectConfig{}, errors.New("controlplane: config: toolchains.collect needs registryNamespace and registryDeployment")
	}
	return CollectConfig{Interval: interval, MinAge: minAge, Keep: c.Keep, RegistryNamespace: c.RegistryNamespace, RegistryDeployment: c.RegistryDeployment}, nil
}

// Enabled reports whether toolchain images are configured.
func (t ToolchainConfig) Enabled() bool { return t.RegistryURL != "" }

func (fc fileConfig) toolchain() (ToolchainConfig, error) {
	t := fc.Toolchains
	if t.RegistryURL == "" {
		return ToolchainConfig{}, nil
	}
	cpu, err1 := resource.ParseQuantity(t.CPU)
	mem, err2 := resource.ParseQuantity(t.Memory)
	scratch, err3 := resource.ParseQuantity(t.ScratchSize)
	deadline, err4 := time.ParseDuration(t.Deadline)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		return ToolchainConfig{}, fmt.Errorf("controlplane: config: toolchains cpu %q, memory %q, scratchSize %q or deadline %q invalid: %w",
			t.CPU, t.Memory, t.ScratchSize, t.Deadline, err)
	}
	cfg := ToolchainConfig{
		RegistryURL: t.RegistryURL, PullHost: t.PullHost,
		Build: images.BuildConfig{
			Namespace: t.Namespace, RuntimeClassName: t.RuntimeClass,
			ServiceAccountName: t.ServiceAccount, PushHost: t.PushHost, EgressProxyURL: t.EgressProxyURL, NoProxy: t.NoProxy,
			Deadline: deadline, ScratchSize: scratch,
			Limits: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem},
		},
	}
	if t.PullHost == "" {
		return ToolchainConfig{}, errors.New("controlplane: config: toolchains.pullHost is required with toolchains.registryURL")
	}
	if cfg.Build.CPURequest, err1 = cpuRequest("toolchains", t.CPURequest, cfg.Build.Limits); err1 != nil {
		return ToolchainConfig{}, err1
	}
	if cfg.Collect, err1 = fc.collect(); err1 != nil {
		return ToolchainConfig{}, err1
	}
	return cfg, nil
}

// LoadConfigFile reads the non-secret configuration into cfg.
func LoadConfigFile(path string, cfg *Config) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("controlplane: config: %w", err)
	}
	var fc fileConfig
	if err := json.Unmarshal(raw, &fc); err != nil {
		return fmt.Errorf("controlplane: config %s: %w", path, err)
	}
	for i := range fc.Repos {
		if fc.Repos[i].BaseBranch == "" {
			fc.Repos[i].BaseBranch = "main"
		}
	}
	cfg.Repos = fc.Repos
	cfg.IntakeLabel = fc.IntakeLabel
	if fc.MaxConcurrentIssues < 0 {
		return fmt.Errorf("controlplane: config: maxConcurrentIssues %d is negative", fc.MaxConcurrentIssues)
	}
	cfg.MaxConcurrentIssues = max(fc.MaxConcurrentIssues, 1)
	cfg.TemporalAddress, cfg.TemporalNamespace = fc.Temporal.Address, fc.Temporal.Namespace
	cfg.GatewayAdminURL = fc.GatewayAdminURL
	cfg.EgressProjectAllow = fc.EgressProjectAllow

	var poll, deadline, session, idle time.Duration
	for _, d := range []struct {
		raw string
		out *time.Duration
	}{{fc.PollInterval, &poll}, {fc.Agent.PodDeadline, &deadline}, {fc.Agent.SessionTimeout, &session}, {fc.Agent.IdleTimeout, &idle}} {
		if d.raw == "" {
			continue
		}
		v, err := time.ParseDuration(d.raw)
		if err != nil {
			return fmt.Errorf("controlplane: config: duration %q: %w", d.raw, err)
		}
		*d.out = v
	}
	cfg.PollInterval = poll
	a := fc.Agent
	agent := activities.AgentConfig{
		Namespace: a.Namespace, RunnerImage: a.RunnerImage, RuntimeClass: a.RuntimeClass, ServiceAccount: a.ServiceAccount,
		EgressProxyURL: a.EgressProxyURL, NoProxy: a.NoProxy, PodDeadline: deadline, GitProxyURL: a.GitProxyURL,
		GatewayURL: a.GatewayURL, Model: config.GatewayModel(a.Model), BudgetUSD: a.BudgetUSD, IssueBudgetUSD: a.IssueBudgetUSD, SessionTimeout: session, IdleTimeout: idle,
		AuthorName: a.AuthorName, AuthorEmail: a.AuthorEmail,
	}
	if a.CPU != "" || a.Memory != "" {
		cpu, err1 := resource.ParseQuantity(a.CPU)
		mem, err2 := resource.ParseQuantity(a.Memory)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("controlplane: config: agent cpu %q / memory %q invalid", a.CPU, a.Memory)
		}
		agent.Limits = corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem}
	}
	if a.WorkspaceSize != "" {
		q, err := resource.ParseQuantity(a.WorkspaceSize)
		if err != nil {
			return fmt.Errorf("controlplane: config: workspace size %q: %w", a.WorkspaceSize, err)
		}
		agent.WorkspaceSize = q
	}
	if agent.CPURequest, err = cpuRequest("agent", a.CPURequest, agent.Limits); err != nil {
		return err
	}
	if agent.ReviewerCPURequest, err = cpuRequest("agent reviewer", firstNonEmpty(a.ReviewerCPU, a.CPURequest), agent.Limits); err != nil {
		return err
	}
	if a.ReviewerMemory != "" {
		q, err := resource.ParseQuantity(a.ReviewerMemory)
		if err != nil || q.Sign() <= 0 {
			return fmt.Errorf("controlplane: config: agent reviewerMemory %q invalid", a.ReviewerMemory)
		}
		agent.ReviewerMemory = q
	}
	cfg.Agent = agent
	if cfg.Gate, err = fc.gate(); err != nil {
		return err
	}
	if cfg.Services, err = fc.services(); err != nil {
		return err
	}
	if cfg.Toolchain, err = fc.toolchain(); err != nil {
		return err
	}
	if err := validateSandboxEnv(fc.SandboxEnv); err != nil {
		return err
	}
	cfg.Agent.Env = fc.SandboxEnv
	cfg.Toolchain.Build.Env = fc.SandboxEnv
	if err := sandbox.ValidateNodeSelector(fc.SandboxNodeSelector); err != nil {
		return fmt.Errorf("controlplane: config: sandboxNodeSelector: %w", err)
	}
	cfg.Agent.NodeSelector = fc.SandboxNodeSelector
	cfg.Toolchain.Build.NodeSelector = fc.SandboxNodeSelector
	if err := sandbox.ValidateTolerations(fc.SandboxTolerations); err != nil {
		return fmt.Errorf("controlplane: config: sandboxTolerations: %w", err)
	}
	cfg.Agent.Tolerations = fc.SandboxTolerations
	cfg.Toolchain.Build.Tolerations = fc.SandboxTolerations
	headroom, err := guestMemoryHeadroom(fc.SandboxGuestMemoryHeadroom)
	if err != nil {
		return err
	}
	cfg.Agent.GuestMemoryHeadroom = headroom
	cfg.Toolchain.Build.GuestMemoryHeadroom = headroom
	if cfg.Models, err = fc.models(); err != nil {
		return err
	}
	cfg.Agent.ModelAPIs = cfg.Models.Providers.GatewayAPIs()
	if err := fc.loadAuth(&cfg.Auth); err != nil {
		return err
	}
	return nil
}

func (fc fileConfig) loadAuth(out *AuthConfig) error {
	a := fc.Auth
	out.PublicURL = strings.TrimRight(strings.TrimSpace(a.PublicURL), "/")
	out.AllowedOrigins = a.AllowedOrigins
	out.SecureCookie = a.SecureCookie
	out.PreviewMode = a.PreviewMode
	if out.PreviewMode && out.PublicURL != "" {
		return errors.New("controlplane: config: auth.previewMode cannot be enabled with auth.publicURL")
	}
	if a.SessionTTL != "" {
		d, err := time.ParseDuration(a.SessionTTL)
		if err != nil || d <= 0 {
			return fmt.Errorf("controlplane: config: auth.sessionTTL %q invalid", a.SessionTTL)
		}
		out.SessionTTL = d
	}
	role := domain.Role(strings.TrimSpace(a.Allowlist.DefaultRole))
	if role != "" && role != domain.RoleViewer && role != domain.RoleMember {
		return fmt.Errorf("controlplane: config: auth.allowlist.defaultRole %q must be viewer or member", role)
	}
	out.Allowlist = auth.Allowlist{
		EmailDomains: a.Allowlist.EmailDomains,
		GitHubOrgs:   a.Allowlist.GitHubOrgs,
		DefaultRole:  role,
	}
	out.GoogleEnabled = a.SSO.Google.Enabled
	out.GoogleIssuerURL = strings.TrimSpace(a.SSO.Google.IssuerURL)
	out.GitLabEnabled = a.SSO.GitLab.Enabled
	out.GitLabIssuerURL = strings.TrimSpace(a.SSO.GitLab.IssuerURL)
	out.GitHubEnabled = a.SSO.GitHub.Enabled
	return nil
}

// models validates the provider profiles and the default model, which they
// must serve: a default nobody serves would fail every run's key.
func (fc fileConfig) models() (Models, error) {
	if err := fc.Providers.Validate(); err != nil {
		return Models{}, fmt.Errorf("controlplane: config: %w", err)
	}
	m := Models{Providers: fc.Providers, Default: fc.Agent.Model}
	if m.Default != "" && !m.Providers.Serves(m.Default) {
		return Models{}, fmt.Errorf("controlplane: config: agent.model %q must be a model ref <provider>/<model> served by providers", m.Default)
	}
	return m, nil
}
