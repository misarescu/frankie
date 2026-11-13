package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/misarescu/frankie/internal/images"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// RepoConfig is a project repo the factory serves and takes issues from.
type RepoConfig struct {
	Name       string `json:"name"`       // factory repo name; the GitHub owner/name in M1
	Upstream   string `json:"upstream"`   // git URL the git proxy forwards to
	KeyPrefix  string `json:"keyPrefix"`  // issue key prefix (D22)
	BaseBranch string `json:"baseBranch"` // default "main"
}

// fileConfig is the non-secret configuration, rendered by the Helm chart.
type fileConfig struct {
	Repos        []RepoConfig `json:"repos"`
	IntakeLabel  string       `json:"intakeLabel"`
	PollInterval string       `json:"pollInterval"`
	Temporal     struct {
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
		WorkspaceSize  string   `json:"workspaceSize"`
		PodDeadline    string   `json:"podDeadline"`
		GitProxyURL    string   `json:"gitProxyURL"`
		GatewayURL     string   `json:"gatewayURL"`
		Model          string   `json:"model"`
		BudgetUSD      float64  `json:"budgetUSD"`
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
		WorkspaceSize string   `json:"workspaceSize"`
		Deadline      string   `json:"deadline"`
	} `json:"gate"`
	// SandboxEnv is set in every agent, gate and toolchain build pod, e.g.
	// GOPROXY for the in-cluster Go module proxy (D37).
	SandboxEnv map[string]string `json:"sandboxEnv"`
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
		BuildKitImage  string   `json:"buildkitImage"`
		RuntimeClass   string   `json:"runtimeClass"`
		ServiceAccount string   `json:"serviceAccount"`
		EgressProxyURL string   `json:"egressProxyURL"`
		NoProxy        []string `json:"noProxy"`
		CPU            string   `json:"cpu"`
		Memory         string   `json:"memory"`
		ScratchSize    string   `json:"scratchSize"`
		Deadline       string   `json:"deadline"`
	} `json:"toolchains"`
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
	return out, nil
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
			Namespace: t.Namespace, BuildKitImage: t.BuildKitImage, RuntimeClassName: t.RuntimeClass,
			ServiceAccountName: t.ServiceAccount, PushHost: t.PushHost, EgressProxyURL: t.EgressProxyURL, NoProxy: t.NoProxy,
			Deadline: deadline, ScratchSize: scratch,
			Limits: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem},
		},
	}
	if t.PullHost == "" {
		return ToolchainConfig{}, errors.New("controlplane: config: toolchains.pullHost is required with toolchains.registryURL")
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
	cfg.TemporalAddress, cfg.TemporalNamespace = fc.Temporal.Address, fc.Temporal.Namespace
	cfg.GatewayAdminURL = fc.GatewayAdminURL

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
		GatewayURL: a.GatewayURL, Model: a.Model, BudgetUSD: a.BudgetUSD, SessionTimeout: session, IdleTimeout: idle,
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
	return nil
}
