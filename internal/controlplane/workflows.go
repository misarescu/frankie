package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/egress"
	"github.com/misarescu/frankie/internal/gateway"
	"github.com/misarescu/frankie/internal/gitproxy"
	"github.com/misarescu/frankie/internal/images"
	"github.com/misarescu/frankie/internal/intake"
	"github.com/misarescu/frankie/internal/mergequeue"
	"github.com/misarescu/frankie/internal/scheduler"
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
	// egressAllowed says whether projects may add egress hosts (D80).
	egressAllowed bool
}

// schedulerInterval is how often the scheduler looks for issues to start.
const schedulerInterval = 5 * time.Second

func (t temporalStarter) StartIssueRun(ctx context.Context, in activities.IssueRunInput) error {
	if n, ok := github.IssueNumber(in.SourceURL); ok && in.SourceNumber == 0 {
		in.SourceNumber = n // the change request then closes the GitHub issue
	}
	wf, arg, err := chooseWorkflow(ctx, t.files, t.models, t.egressAllowed, in)
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

// StartRefinement starts the issue's refinement (D66) with its snapshot. An
// invalid configuration still starts Refine, which records the reason on the
// issue; a failure to read the repo returns an error, and the scheduler tries again.
func (t temporalStarter) StartRefinement(ctx context.Context, in activities.IssueRunInput) error {
	snap, err := snapshot.ResolveRefinement(ctx, t.files, in.Repo, in.BaseBranch)
	if errors.Is(err, snapshot.ErrInvalid) {
		snap, err = snapshot.Snapshot{Error: err.Error()}, nil
	}
	if err != nil {
		return err
	}
	snap, in = withEgress(snap, in, t.egressAllowed)
	_, err = t.c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       "refine-" + in.IssueKey,
		TaskQueue:                workflow.TaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}, workflow.Refine, workflow.RefineInput{Issue: in, Snapshot: snap})
	return err
}

// chooseWorkflow snapshots what the issue's run will execute (invariant 4) and
// runs it with Interpret: the project's workflow, or the default one. An invalid
// or not yet runnable configuration, or one naming models the gateway does not
// serve, still starts Interpret, which fails the run with the reason so it
// shows up on the run. A failure to read the repo returns an error, and intake
// tries again on its next poll. IssueRun is no longer started; it stays
// registered until its runs drain.
func chooseWorkflow(ctx context.Context, files snapshot.FileReader, models Models, egressAllowed bool, in activities.IssueRunInput) (any, any, error) {
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
	snap, in = withEgress(snap, in, egressAllowed)
	return workflow.Interpret, workflow.InterpretInput{Issue: in, Snapshot: snap}, nil
}

// withEgress hands the run's activities the hosts its project adds to the
// egress list (D80), or fails the run with the reason when this deployment
// does not let projects add any.
func withEgress(snap snapshot.Snapshot, in activities.IssueRunInput, allowed bool) (snapshot.Snapshot, activities.IssueRunInput) {
	if snap.Error != "" || len(snap.EgressAllow) == 0 {
		return snap, in
	}
	if !allowed {
		return snapshot.Snapshot{Error: "the project adds egress hosts (egress.allow in " + config.ProjectConfigPath +
			"), which this deployment does not allow (egressProxy.projectAllow)"}, in
	}
	in.EgressAllow = snap.EgressAllow
	return snap, in
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
	w.RegisterWorkflow(workflow.Refine)
	var egressSigner *egress.Signer
	if cfg.EgressProjectAllow {
		if egressSigner, err = egress.NewSigner(cfg.EgressKey); err != nil {
			return nil, nil, fmt.Errorf("controlplane: egress key: %w", err)
		}
	}
	w.RegisterActivity(&activities.Activities{Store: st, Kube: cfg.Kube, Gateway: gw, Signer: signer, GitHub: gh, Toolchain: toolchain, Agent: cfg.Agent, Gate: cfg.Gate, Services: cfg.Services,
		Egress: egressSigner, Log: log.With("component", "activities")})
	if err := w.Start(); err != nil {
		c.Close()
		return nil, nil, fmt.Errorf("controlplane: worker: %w", err)
	}
	log.Info("temporal worker started", "address", cfg.TemporalAddress, "taskQueue", workflow.TaskQueue)

	if cfg.GitHubToken != "" {
		mw := &mergequeue.Watcher{Store: st, Source: gh, Interval: max(cfg.PollInterval, 10*time.Second),
			Log: log.With("component", "mergequeue")}
		go mw.Run(ctx)
		closer := &intake.Closer{Store: st, Source: gh, Interval: max(cfg.PollInterval, 10*time.Second),
			Log: log.With("component", "closer")}
		go closer.Run(ctx)
	}
	if cfg.GitHubToken != "" && cfg.IntakeLabel != "" {
		in := &intake.Intake{Store: st, Source: gh, Config: gh, Projects: projects,
			Label: cfg.IntakeLabel, Interval: max(cfg.PollInterval, 10*time.Second), Log: log.With("component", "intake")}
		go in.Run(ctx)
		log.Info("intake polling", "label", cfg.IntakeLabel, "interval", in.Interval)
	}
	sch := &scheduler.Scheduler{Store: st, Starter: temporalStarter{c: c, files: gh, models: cfg.Models, egressAllowed: egressSigner != nil},
		Projects: make(map[uuid.UUID]scheduler.Project, len(projects)), Limit: cfg.MaxConcurrentIssues,
		Interval: schedulerInterval, Log: log.With("component", "scheduler")}
	for _, p := range projects {
		sch.Projects[p.ID] = scheduler.Project{Repo: p.Repo, BaseBranch: p.BaseBranch}
	}
	go sch.Run(ctx)
	log.Info("scheduler running", "maxConcurrentIssues", sch.Limit)
	return c, func() { w.Stop(); c.Close() }, nil
}
