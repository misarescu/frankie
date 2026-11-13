package controlplane

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/misarescu/frankie/internal/workflow"
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
	agent := workflow.AgentConfig{
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
	return nil
}
