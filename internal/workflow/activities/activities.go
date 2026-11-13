// Package activities holds the Temporal Activities of the factory's workflows:
// all of their I/O (store, Kubernetes, runner, gateway, GitHub). Every Activity
// is idempotent, because Temporal retries them. One Activity per file.
package activities

import (
	"encoding/json"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/gateway"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/images"
	"github.com/misarescu/frankie/internal/store"
)

// IssueRunInput is everything the run needs, snapshotted at start (invariant 4).
type IssueRunInput struct {
	IssueID            string
	IssueKey           string // e.g. gc-1
	Title              string
	Spec               string
	AcceptanceCriteria []string
	SourceURL          string // the GitHub issue
	SourceNumber       int
	Repo               string // factory repo, also the GitHub owner/name in M1
	BaseBranch         string
}

// StartRunResult identifies the run and its single M1 step.
type StartRunResult struct {
	RunID     string
	StepRunID string
	Branch    string
	// ToolchainImage is the repo's toolchain image for the agent pod; empty
	// means the platform base image. The workflow sets it after StartRun, from
	// EnsureToolchainImage.
	ToolchainImage string
}

// AgentPod is a launched agent pod.
type AgentPod struct {
	Name string
	IP   string
}

// SessionResult is the runner's done event, as the workflow needs it.
type SessionResult struct {
	Status    string // succeeded | failed | aborted | timeout
	Reason    string
	Result    string // the agent's final message
	Branch    string
	CommitSHA string
	Pushed    bool
}

// FinishInput records a run's outcome.
type FinishInput struct {
	IssueID   string
	RunID     string
	StepRunID string
	Succeeded bool
	Reason    string
}

// AgentConfig describes how agent steps run.
type AgentConfig struct {
	Namespace      string
	RunnerImage    string
	RuntimeClass   string
	ServiceAccount string
	EgressProxyURL string
	NoProxy        []string
	Limits         corev1.ResourceList
	WorkspaceSize  resource.Quantity
	PodDeadline    time.Duration
	GitProxyURL    string // as agents reach it, e.g. http://frankie-controlplane.frankie.svc:8081
	GatewayURL     string // as agents reach it, e.g. http://frankie-llm-gateway.frankie.svc:4000
	Model          string
	BudgetUSD      float64
	SessionTimeout time.Duration
	IdleTimeout    time.Duration
	AuthorName     string
	AuthorEmail    string
}

// Activities do the workflow's I/O. Every Activity is idempotent.
type Activities struct {
	Store   *store.Store
	Kube    kubernetes.Interface
	Gateway *gateway.Client
	Signer  *gitproxy.Signer
	GitHub  *github.Client
	// Toolchain builds and finds toolchain images (D30); nil disables them and
	// agent pods run in the platform base image.
	Toolchain *images.Ensurer
	Agent     AgentConfig
	Log       *slog.Logger
}

// hardcodedWorkflow is the snapshot recorded on M1 runs (invariant 4).
var hardcodedWorkflow = json.RawMessage(`{"name":"m1-issue-run","version":"1","steps":[{"id":"implement","kind":"agent"},{"id":"open-change-request","kind":"action"}]}`)
