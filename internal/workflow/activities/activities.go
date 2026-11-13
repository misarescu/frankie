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
	"github.com/misarescu/frankie/internal/domain"
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

// StartRunResult identifies the run and the agent step being executed: the
// single M1 step for IssueRun, one execution of an agent step for Interpret.
type StartRunResult struct {
	RunID     string
	StepRunID string
	Branch    string
	// ToolchainImage is the repo's toolchain image for the agent pod; empty
	// means the platform base image. The workflow sets it after StartRun, from
	// EnsureToolchainImage.
	ToolchainImage string
	// Step is the agent step from the workflow definition (Interpret). IssueRun
	// leaves it nil and gets the M2 step: the implement prompt and the platform's
	// model, budget and timeout.
	Step *AgentStep `json:",omitempty"`
}

// AgentStep is one execution of a definition's agent step, resolved by the
// workflow from its snapshot.
type AgentStep struct {
	StepID       string // e.g. implement
	Role         string
	RoleVersion  int
	SystemPrompt string
	// Model is the gateway's model name; empty means the platform's.
	Model     string
	BudgetUSD float64       // 0: the platform's
	Timeout   time.Duration // 0: the platform's session timeout
	Inputs    []string
	// Feedback is what the gate that looped back to this step found, when the
	// step takes the feedback input.
	Feedback string
	// AllowedTools are the role's harness-neutral tools; empty means all.
	AllowedTools []string `json:",omitempty"`
	// Contract is the role's output contract, e.g. review-verdict. A step with
	// a contract reviews: its workspace is read-only and its final message is
	// the result.
	Contract string `json:",omitempty"`
}

// Review is one reviewer branch's session, for an agent-verdict gate.
type Review struct {
	StepID    string
	Role      string
	StepRunID string
	CommitSHA string // the commit the reviewer checked out
	Result    string // the reviewer's final message, which should be the verdict
}

// VerdictGateRequest is one execution of an agent-verdict gate.
type VerdictGateRequest struct {
	RunID     string
	StepRunID string
	StepID    string
	Require   string // all, any or majority
	Policy    domain.GatePolicy
	CommitSHA string // the commit under review
	Reviews   []Review
}

// StepStart identifies one execution of a step; StartStep creates its StepRun.
type StepStart struct {
	RunID   string
	StepID  string
	Kind    domain.StepKind
	Attempt int // 1 for the first execution, +1 per loop back
}

// StepEnd records how a step's execution ended.
type StepEnd struct {
	StepRunID string
	Succeeded bool
}

// GateRequest is one execution of a definition's command gate.
type GateRequest struct {
	RunID     string
	StepRunID string
	StepID    string
	Commands  []string
	Timeout   time.Duration // 0: the platform's gate deadline
	Branch    string
	CommitSHA string
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
	// AgentSucceeded marks the agent step succeeded even though the run failed,
	// e.g. when a later gate failed (M2). Runs from before it leave it false.
	AgentSucceeded bool
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
	// Env is set in agent and gate pods, e.g. GOPROXY for the in-cluster Go
	// module proxy (D37).
	Env map[string]string
}

// GateConfig describes how command gates run (docs/spec/03). They share the
// agents' namespace, runtime class, service account and egress proxy.
type GateConfig struct {
	Commands      []string // default: mise run lint, mise run test
	Limits        corev1.ResourceList
	WorkspaceSize resource.Quantity
	Deadline      time.Duration
}

// DefaultGateCommands are the M2 command gate: generic across repos because the
// repo's mise.toml defines what lint and test mean (docs/spec/05).
var DefaultGateCommands = []string{"mise run lint", "mise run test"}

// GuardRequest asks which changes between two commits need a human
// (invariant 6): BaseSHA is where the run started, HeadSHA its last push.
type GuardRequest struct {
	BaseSHA  string
	HeadSHA  string
	Patterns []string // protected-path patterns from the snapshot
}

// HumanGateRequest opens a human gate: the decision a person is asked for.
type HumanGateRequest struct {
	RunID            string
	StepRunID        string
	StepID           string
	Reason           string // what is asked, shown in the approvals inbox
	CommitSHA        string
	ChangeRequestURL string
	ExpiresAt        time.Time
}

// HumanGateClose records how a human gate ended.
type HumanGateClose struct {
	StepRunID string
	Status    domain.ApprovalStatus // approved, rejected or timed_out
	By        string
	Comment   string
}

// GateOutcome is a command gate's verdict as the workflow needs it.
type GateOutcome struct {
	StepRunID string
	Passed    bool
	Summary   string // e.g. "mise run test exited 1"
	// Feedback tells the agent a loop goes back to what failed: the failed
	// commands with the tail of their output, or the reviewers' findings.
	Feedback string `json:",omitempty"`
	// Inconclusive means the gate could not judge the change (a reviewer
	// returned no valid verdict); looping back to the coder would not help.
	Inconclusive bool `json:",omitempty"`
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
	Gate      GateConfig
	Services  ServicesConfig
	Log       *slog.Logger
}

// hardcodedWorkflow is the snapshot recorded on M1 runs (invariant 4).
var hardcodedWorkflow = json.RawMessage(`{"name":"m1-issue-run","version":"1","steps":[{"id":"implement","kind":"agent"},{"id":"open-change-request","kind":"action"}]}`)
