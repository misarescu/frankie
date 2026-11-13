package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/gateway"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/images"
	"github.com/misarescu/frankie/internal/intake"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// ensureProjects creates a project per configured repo (idempotent).
func ensureProjects(ctx context.Context, st *store.Store, repos []RepoConfig) ([]intake.Project, error) {
	out := make([]intake.Project, 0, len(repos))
	for _, r := range repos {
		p, err := st.GetProjectByName(ctx, r.Name)
		if errors.Is(err, store.ErrNotFound) {
			p, err = st.CreateProject(ctx, r.Name, r.KeyPrefix)
		}
		if err != nil {
			return nil, fmt.Errorf("controlplane: project %s: %w", r.Name, err)
		}
		if r.KeyPrefix != "" && p.KeyPrefix != r.KeyPrefix {
			return nil, fmt.Errorf("controlplane: project %s has key prefix %q, config says %q (prefixes never change)", r.Name, p.KeyPrefix, r.KeyPrefix)
		}
		out = append(out, intake.Project{ID: p.ID, Repo: r.Name, BaseBranch: r.BaseBranch})
	}
	return out, nil
}

// temporalStarter starts one workflow per issue: the ID is stable, so a start
// while a run is in flight returns the existing one.
type temporalStarter struct {
	c      client.Client
	files  snapshot.FileReader
	models Models
}

func (t temporalStarter) StartIssueRun(ctx context.Context, in activities.IssueRunInput) error {
	wf, arg, err := chooseWorkflow(ctx, t.files, t.models, in)
	if err != nil {
		return err
	}
	_, err = t.c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       "issue-" + in.IssueKey,
		TaskQueue:                workflow.TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}, wf, arg)
	return err
}

// chooseWorkflow snapshots what the issue's run will execute (invariant 4) and
// runs it with Interpret: the project's workflow, or the default one. An invalid
// or not yet runnable configuration, or one naming models the gateway does not
// serve, still starts Interpret, which fails the run with the reason so it
// shows up on the run. A failure to read the repo returns an error, and intake
// tries again on its next poll. IssueRun is no longer started; it stays
// registered until its runs drain.
func chooseWorkflow(ctx context.Context, files snapshot.FileReader, models Models, in activities.IssueRunInput) (any, any, error) {
	snap, err := snapshot.Resolve(ctx, files, in.Repo, in.BaseBranch)
	switch {
	case errors.Is(err, snapshot.ErrInvalid):
		snap = snapshot.Snapshot{Error: err.Error()}
	case err != nil:
		return nil, nil, err
	default:
		if err := workflow.CheckSupported(snap.Workflow); err != nil {
			snap = snapshot.Snapshot{Error: "the interpreter cannot run this yet: " + err.Error()}
		} else if err := config.CheckModels(snap.Workflow, snap.Roles, models.Default, models.Providers); err != nil {
			snap = snapshot.Snapshot{Error: "the platform does not serve this workflow's models: " + err.Error()}
		}
	}
	return workflow.Interpret, workflow.InterpretInput{Issue: in, Snapshot: snap}, nil
}

// startWorkflows connects to Temporal, runs the worker and the intake loop. It
// returns the client, which the API uses to signal human gate decisions.
func startWorkflows(ctx context.Context, cfg Config, st *store.Store, signer *gitproxy.Signer, projects []intake.Project, log *slog.Logger) (client.Client, func(), error) {
	if cfg.Kube == nil {
		return nil, nil, errors.New("controlplane: workflows need Kubernetes access to launch agent pods")
	}
	gw, err := gateway.New(cfg.GatewayAdminURL, cfg.GatewayMasterKey)
	if err != nil {
		return nil, nil, err
	}
	var c client.Client
	deadline := time.Now().Add(max(cfg.DBWait, 2*time.Minute))
	for {
		c, err = client.Dial(client.Options{HostPort: cfg.TemporalAddress, Namespace: cfg.TemporalNamespace,
			Logger: tlog.NewStructuredLogger(log.With("component", "temporal"))})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("controlplane: temporal not reachable: %w", err)
		}
		log.Info("waiting for temporal", "error", err)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	gh := github.NewClient(cfg.GitHubToken, "")
	var toolchain *images.Ensurer
	if cfg.Toolchain.Enabled() {
		toolchain = &images.Ensurer{
			Registry: images.HTTPRegistry{BaseURL: cfg.Toolchain.RegistryURL, Client: &http.Client{Timeout: 15 * time.Second}},
			Digests:  store.ToolchainDigests{Store: st},
			Builder: images.KubeBuilder{Kube: cfg.Kube, Config: cfg.Toolchain.Build,
				Heartbeat: func(ctx context.Context, details ...any) { activity.RecordHeartbeat(ctx, details...) }},
			PullHost: cfg.Toolchain.PullHost,
		}
	}
	// On shutdown, give running Activities time to finish before the pod exits
	// (terminationGracePeriodSeconds in the chart is longer).
	w := worker.New(c, workflow.TaskQueue, worker.Options{WorkerStopTimeout: 30 * time.Second})
	w.RegisterWorkflow(workflow.IssueRun)
	w.RegisterWorkflow(workflow.Interpret)
	w.RegisterActivity(&activities.Activities{Store: st, Kube: cfg.Kube, Gateway: gw, Signer: signer, GitHub: gh, Toolchain: toolchain, Agent: cfg.Agent, Gate: cfg.Gate, Services: cfg.Services,
		Log: log.With("component", "activities")})
	if err := w.Start(); err != nil {
		c.Close()
		return nil, nil, fmt.Errorf("controlplane: worker: %w", err)
	}
	log.Info("temporal worker started", "address", cfg.TemporalAddress, "taskQueue", workflow.TaskQueue)

	if cfg.GitHubToken != "" && cfg.IntakeLabel != "" {
		in := &intake.Intake{Store: st, Source: gh, Starter: temporalStarter{c: c, files: gh, models: cfg.Models}, Projects: projects,
			Label: cfg.IntakeLabel, Interval: max(cfg.PollInterval, 10*time.Second), Log: log.With("component", "intake")}
		go in.Run(ctx)
		log.Info("intake polling", "label", cfg.IntakeLabel, "interval", in.Interval)
	}
	return c, func() { w.Stop(); c.Close() }, nil
}
