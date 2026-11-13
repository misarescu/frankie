package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/gateway"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/intake"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow"
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

// temporalStarter starts one IssueRun workflow per issue: the ID is stable, so a
// start while a run is in flight returns the existing one.
type temporalStarter struct{ c client.Client }

func (t temporalStarter) StartIssueRun(ctx context.Context, in workflow.IssueRunInput) error {
	_, err := t.c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       "issue-" + in.IssueKey,
		TaskQueue:                workflow.TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}, workflow.IssueRun, in)
	return err
}

// startWorkflows connects to Temporal, runs the worker and the intake loop.
func startWorkflows(ctx context.Context, cfg Config, st *store.Store, signer *gitproxy.Signer, projects []intake.Project, log *slog.Logger) (func(), error) {
	if cfg.Kube == nil {
		return nil, errors.New("controlplane: workflows need Kubernetes access to launch agent pods")
	}
	gw, err := gateway.New(cfg.GatewayAdminURL, cfg.GatewayMasterKey)
	if err != nil {
		return nil, err
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
			return nil, fmt.Errorf("controlplane: temporal not reachable: %w", err)
		}
		log.Info("waiting for temporal", "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	gh := github.NewClient(cfg.GitHubToken, "")
	// On shutdown, give running Activities time to finish before the pod exits
	// (terminationGracePeriodSeconds in the chart is longer).
	w := worker.New(c, workflow.TaskQueue, worker.Options{WorkerStopTimeout: 30 * time.Second})
	w.RegisterWorkflow(workflow.IssueRun)
	w.RegisterActivity(&workflow.Activities{Store: st, Kube: cfg.Kube, Gateway: gw, Signer: signer, GitHub: gh, Agent: cfg.Agent,
		Log: log.With("component", "activities")})
	if err := w.Start(); err != nil {
		c.Close()
		return nil, fmt.Errorf("controlplane: worker: %w", err)
	}
	log.Info("temporal worker started", "address", cfg.TemporalAddress, "taskQueue", workflow.TaskQueue)

	if cfg.GitHubToken != "" && cfg.IntakeLabel != "" {
		in := &intake.Intake{Store: st, Source: gh, Starter: temporalStarter{c}, Projects: projects,
			Label: cfg.IntakeLabel, Interval: max(cfg.PollInterval, 10*time.Second), Log: log.With("component", "intake")}
		go in.Run(ctx)
		log.Info("intake polling", "label", cfg.IntakeLabel, "interval", in.Interval)
	}
	return func() { w.Stop(); c.Close() }, nil
}
