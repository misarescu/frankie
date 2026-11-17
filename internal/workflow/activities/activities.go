// Package activities holds the Temporal Activities of the factory's workflows:
// all of their I/O (store, Kubernetes, runner, gateway, GitHub). Every Activity
// is idempotent, because Temporal retries them. One Activity per file.
package activities

import (
	"cmp"
	"encoding/json"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/drivers"
	"github.com/misarescu/frankie/internal/egress"
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
	// SpecFeedback is why a person sent the issue's last refined spec back, or
	// why the last refinement failed; only refinement passes it (D66).
	SpecFeedback string `json:",omitempty"`
	// Dependencies are the issues this one waits for, its own and its
	// ancestors', as they stood when the run started; only refinement passes
	// them, so the refiner writes the spec against what they deliver (M5.1).
	Dependencies []IssueDependency `json:",omitempty"`
	// EgressAllow are the hosts the project adds to the platform's egress
	// list, from its snapshot (D80); the run's agent, gate and build pods get
	// a token for them.
	EgressAllow []string `json:",omitempty"`
	// Labels are the issue's labels as the run started; some override the
	// project's settings (D92).
	Labels []string `json:",omitempty"`
	// Type is the issue's type (epic, story, task or bug); it picks the
	// workflow when the project names one per type (D94).
	Type string `json:",omitempty"`
}

// IssueDependency is an issue another one depends on, as refinement sees it.
type IssueDependency struct {
	Key    string
	Title  string
	Status string // the issue's status, e.g. merged or awaiting_human
	Spec   string // clipped
	// ChangeRequests are the dependency's change requests, newest first: where
	// its code is before it reaches the base branch.
	ChangeRequests []DependencyChange `json:",omitempty"`
}

// DependencyChange is one change request of a dependency.
type DependencyChange struct {
	URL    string
	Branch string
	Status string // open, merged or closed
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
	// Findings is what the run's investigation found, rendered; the change
	// request's description carries it (D94).
	Findings string `json:",omitempty"`
	// ReleaseID is set when the run is a release's (D129): the session runs
	// the release agent, spends from the release's budget and gets the task
	// PrepareReleaseSession wrote.
	ReleaseID string `json:",omitempty"`
}

// AgentStep is one execution of a definition's agent step, resolved by the
// workflow from its snapshot.
type AgentStep struct {
	StepID       string // e.g. implement
	Role         string
	RoleVersion  int
	SystemPrompt string
	// Model is the gateway's model name; empty means the platform's.
	Model string
	// Harness runs the step (claude-code, opencode); empty means claude-code,
	// as for steps recorded before the field existed.
	Harness   string  `json:",omitempty"`
	BudgetUSD float64 // 0: the platform's
	// IssueBudgetUSD caps all of the issue's sessions together; 0: the
	// platform's (D53).
	IssueBudgetUSD float64       `json:",omitempty"`
	Timeout        time.Duration // 0: the platform's session timeout
	Inputs         []string
	// Feedback is what the gate that looped back to this step found, when the
	// step takes the feedback input.
	Feedback string
	// Findings is what an earlier investigating step found, rendered, when
	// the step takes the findings input (D94).
	Findings string `json:",omitempty"`
	// Alongside are the roles of the other writers of the step's group,
	// which work on the issue at the same time on their own branches (D122).
	Alongside []string `json:",omitempty"`
	// AllowedTools are the role's harness-neutral tools; empty means all.
	AllowedTools []string `json:",omitempty"`
	// Contract is the role's output contract, e.g. review-verdict. A step with
	// a contract reviews: its workspace is read-only and its final message is
	// the result.
	Contract string `json:",omitempty"`
	// AskHuman offers the agent the ask-human tool; the workflow sets it only
	// when it relays answers to the session (D55).
	AskHuman bool `json:",omitempty"`
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
	// ExpectFail: the gate passes when a command fails (D95).
	ExpectFail bool `json:",omitempty"`
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
	// Merged means the merge queue merged the run's change request (D88): the
	// issue becomes merged rather than waiting for a person.
	Merged bool `json:",omitempty"`
}

// AgentConfig describes how agent steps run.
type AgentConfig struct {
	Namespace      string
	RunnerImage    string
	RuntimeClass   string
	ServiceAccount string
	EgressProxyURL string
	NoProxy        []string
	// NodeSelector keeps agent, gate and branch update pods on the agent pool (D100).
	NodeSelector map[string]string
	// Tolerations let them onto that pool when it is tainted to keep other pods off (D101).
	Tolerations []corev1.Toleration
	// GuestMemoryHeadroom is VM memory beyond each pod's container limits,
	// for the guest kernel and agent (D103); zero adds none.
	GuestMemoryHeadroom resource.Quantity
	Limits              corev1.ResourceList
	WorkspaceSize       resource.Quantity
	PodDeadline         time.Duration
	GitProxyURL         string // as agents reach it, e.g. http://frankie-controlplane.frankie.svc:8081
	GatewayURL          string // as agents reach it, e.g. http://frankie-llm-gateway.frankie.svc:4000
	Model               string
	// IssueBudgetUSD is the default cap on all of an issue's sessions
	// together; projects may set their own (D53).
	IssueBudgetUSD float64
	// ModelAPIs maps the gateway's model names to the API each is served with
	// (D48), so the runner can configure a harness to speak it.
	ModelAPIs      map[string]config.API
	BudgetUSD      float64
	SessionTimeout time.Duration
	IdleTimeout    time.Duration
	AuthorName     string
	AuthorEmail    string
	// Env is set in agent and gate pods, e.g. GOPROXY for the in-cluster Go
	// module proxy (D37).
	Env map[string]string
	// CPURequest and ReviewerCPURequest are what an agent pod, or a read-only
	// one (a reviewer or the refiner), requests of its CPU limit; zero means
	// the limit (D77).
	CPURequest         resource.Quantity
	ReviewerCPURequest resource.Quantity
	// ReviewerMemory is the memory limit of a read-only step's pod (a reviewer
	// or the refiner); zero means the coder's (D104).
	ReviewerMemory resource.Quantity
}

// GateConfig describes how command gates run (docs/spec/03). They share the
// agents' namespace, runtime class, service account and egress proxy.
type GateConfig struct {
	Commands      []string // default: mise run lint, mise run test
	Limits        corev1.ResourceList
	CPURequest    resource.Quantity // zero means the CPU limit (D77)
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
	// CanRequestChanges offers sending the change back to the coder (D92).
	CanRequestChanges bool `json:",omitempty"`
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
	// Egress signs the egress tokens that give a run's pods its project's
	// hosts (D80); nil leaves pods on the platform's list.
	Egress *egress.Signer
	// Toolchain builds and finds toolchain images (D30); nil disables them and
	// agent pods run in the platform base image.
	Toolchain *images.Ensurer
	Agent     AgentConfig
	Gate      GateConfig
	Services  ServicesConfig
	Log       *slog.Logger
	// RunnerAddr is where an agent pod's runner listens; nil means the pod IP
	// on sandbox.RunnerPort. Tests point it at a fake runner.
	RunnerAddr func(pod AgentPod) string
	// SpendSettle is how long to wait between reads of a session's spend
	// while the gateway writes it; default 3s (D53).
	SpendSettle time.Duration
	// SessionPodCheck is how often a session checks that its agent pod still
	// runs; default 15s (D102).
	SessionPodCheck time.Duration
	// Queue starts and signals the merge queue workflows (D88) and the release
	// workflows (D114), on TaskQueue.
	Queue     QueueStarter
	TaskQueue string
	// Drivers are the release drivers stages name (D112); nil has none, so a
	// project's stages are invalid until its drivers exist.
	Drivers *drivers.Registry
	// Verifier checks a stage once it runs a release (verify: e2e-suite);
	// nil makes stages that verify invalid.
	Verifier Verifier
	// Release times the release stages (D114).
	Release ReleaseConfig
	// IntakeLabel is the label intake syncs issues by; the bug issue a failed
	// release opens carries it (D115).
	IntakeLabel string
}

// ReleaseConfig times a release's stages.
type ReleaseConfig struct {
	// StageTimeout bounds waiting for a stage to run a release; default 15m.
	StageTimeout time.Duration
	// PollInterval is how often a driver is asked whether its stage runs the
	// release; default 10s.
	PollInterval time.Duration
	// PreviewTimeout bounds waiting for a preview to run a change request's
	// head; default 10m. RoomTimeout bounds waiting for room for a preview;
	// default 30m (D126).
	PreviewTimeout, RoomTimeout time.Duration
	// AgentBudgetUSD caps what a release's agent sessions spend together;
	// default 1 (D129).
	AgentBudgetUSD float64
}

func (c ReleaseConfig) previewTimeout() time.Duration {
	return cmp.Or(c.PreviewTimeout, 10*time.Minute)
}

func (c ReleaseConfig) roomTimeout() time.Duration { return cmp.Or(c.RoomTimeout, 30*time.Minute) }

func (c ReleaseConfig) stageTimeout() time.Duration {
	if c.StageTimeout > 0 {
		return c.StageTimeout
	}
	return 15 * time.Minute
}

func (c ReleaseConfig) pollInterval() time.Duration {
	if c.PollInterval > 0 {
		return c.PollInterval
	}
	return 10 * time.Second
}

// driverRegistry returns the release drivers, none when unconfigured.
func (a *Activities) driverRegistry() *drivers.Registry {
	if a.Drivers != nil {
		return a.Drivers
	}
	r, _ := drivers.NewRegistry()
	return r
}

// hardcodedWorkflow is the snapshot recorded on M1 runs (invariant 4).
var hardcodedWorkflow = json.RawMessage(`{"name":"m1-issue-run","version":"1","steps":[{"id":"implement","kind":"agent"},{"id":"open-change-request","kind":"action"}]}`)
