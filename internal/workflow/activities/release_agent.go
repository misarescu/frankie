package activities

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/snapshot"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/roles"
)

// The release agent's steps (D129): notes while the release deploys, and the
// analysis of a failed verification.
const (
	ReleaseStepNotes    = "notes"
	ReleaseStepAnalysis = "analysis"
)

// releaseAgentRole is the role the release agent's sessions run.
const releaseAgentRole = "release-agent"

// releaseSessionTimeout bounds one release agent session.
const releaseSessionTimeout = 20 * time.Minute

// ReleaseAgentRequest asks for one session of a release's agent (D129).
type ReleaseAgentRequest struct {
	ReleaseID  string
	Repo       string
	SHA        string
	BaseBranch string
	Step       string // ReleaseStepNotes or ReleaseStepAnalysis
	// Failure is the failed verification an analysis reads.
	Failure *ReleaseFailureReport `json:",omitempty"`
}

// ReleaseSession is a release agent session ready to run with the agent step
// Activities (EnsureStepImage, LaunchAgentPod, RunAgentSession,
// DeleteAgentPod): the release dressed as the issue-shaped input they take,
// and the step of the release's run.
type ReleaseSession struct {
	Issue IssueRunInput
	Run   StartRunResult
}

// PrepareReleaseSession records the session's step on the release's run,
// creating the run on its first session, and returns what the session runs:
// the release-agent role as the repo has it at the release commit (its
// .factory/roles/, else the built-in), with the step's output contract, and
// the task naming the release, the version it builds on and its issues.
func (a *Activities) PrepareReleaseSession(ctx context.Context, req ReleaseAgentRequest) (ReleaseSession, error) {
	relID, err := uuid.Parse(req.ReleaseID)
	if err != nil {
		return ReleaseSession{}, temporal.NewNonRetryableApplicationError("bad release id", "input", err)
	}
	contract := map[string]string{ReleaseStepNotes: config.ContractReleaseNotes, ReleaseStepAnalysis: config.ContractReleaseAnalysis}[req.Step]
	if contract == "" || (req.Step == ReleaseStepAnalysis && req.Failure == nil) {
		return ReleaseSession{}, temporal.NewNonRetryableApplicationError("unknown release agent step "+req.Step, "input", nil)
	}
	rel, err := a.Store.GetRelease(ctx, relID)
	if err != nil {
		return ReleaseSession{}, err
	}
	role, err := snapshot.RoleAt(ctx, a.GitHub, req.Repo, req.SHA, releaseAgentRole)
	if errors.Is(err, snapshot.ErrInvalid) {
		return ReleaseSession{}, temporal.NewNonRetryableApplicationError(err.Error(), "config", err)
	}
	if err != nil {
		return ReleaseSession{}, err
	}
	system, err := roles.WithContract(role.SystemPrompt, contract)
	if err != nil {
		return ReleaseSession{}, temporal.NewNonRetryableApplicationError(err.Error(), "config", err)
	}
	issues, err := a.releaseIssues(ctx, rel)
	if err != nil {
		return ReleaseSession{}, err
	}
	var task string
	if req.Step == ReleaseStepNotes {
		prev, err := a.previousVersion(ctx, rel)
		if err != nil {
			return ReleaseSession{}, err
		}
		task = notesTask(rel, prev, issues)
	} else {
		task = analysisTask(rel, *req.Failure, issues)
	}

	run, err := a.releaseRun(ctx, rel, req.Repo)
	if err != nil {
		return ReleaseSession{}, err
	}
	step, err := a.stepRun(ctx, store.NewStepRun{RunID: run.ID, StepID: req.Step, Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		return ReleaseSession{}, err
	}
	key := "release-" + short(rel.SHA)
	return ReleaseSession{
		Issue: IssueRunInput{IssueKey: key, Title: "Release " + short(rel.SHA), Spec: task, Repo: req.Repo, BaseBranch: req.BaseBranch},
		Run: StartRunResult{RunID: run.ID.String(), StepRunID: step.ID.String(), Branch: "agent/" + key + "/" + req.Step, ReleaseID: rel.ID.String(),
			Step: &AgentStep{StepID: req.Step, Role: role.Name, RoleVersion: role.Version, SystemPrompt: system,
				Model: config.GatewayModel(role.DefaultModel), BudgetUSD: a.Release.agentBudget(), Timeout: releaseSessionTimeout,
				Inputs: []string{"spec"}, AllowedTools: role.AllowedTools, Contract: contract}},
	}, nil
}

// releaseRun is the release's run, created with its first session (D129).
func (a *Activities) releaseRun(ctx context.Context, rel domain.Release, repo string) (domain.Run, error) {
	runs, err := a.Store.ListReleaseRuns(ctx, rel.ID)
	if err != nil {
		return domain.Run{}, err
	}
	run := domain.Run{}
	if len(runs) > 0 {
		run = runs[0]
	} else if run, err = a.Store.CreateRun(ctx, store.NewRun{ReleaseID: rel.ID, WorkflowSnapshot: json.RawMessage(`{}`),
		WorkflowVersion: releaseAgentRole, TemporalWorkflowID: ReleaseWorkflowID(repo)}); err != nil {
		return domain.Run{}, err
	}
	if run.Status == domain.ExecPending {
		if run, err = a.Store.TransitionRun(ctx, run.ID, domain.ExecPending, domain.ExecRunning); err != nil {
			return domain.Run{}, err
		}
	}
	return run, nil
}

// FinishReleaseRun ends the release's run, if it has one: succeeded when
// every session succeeded.
func (a *Activities) FinishReleaseRun(ctx context.Context, releaseID string) error {
	relID, err := uuid.Parse(releaseID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("bad release id", "input", err)
	}
	runs, err := a.Store.ListReleaseRuns(ctx, relID)
	if err != nil || len(runs) == 0 || runs[0].Status != domain.ExecRunning {
		return err
	}
	steps, err := a.Store.ListStepRuns(ctx, runs[0].ID)
	if err != nil {
		return err
	}
	status := domain.ExecSucceeded
	for _, s := range steps {
		if s.Status != domain.ExecSucceeded {
			status = domain.ExecFailed
		}
	}
	_, err = a.Store.TransitionRun(ctx, runs[0].ID, domain.ExecRunning, status)
	return err
}

// releaseIssue is an issue of a release as the agent reads it.
type releaseIssue struct {
	issue  domain.Issue
	change domain.ChangeRequest // its merged change request, if any
}

func (a *Activities) releaseIssues(ctx context.Context, rel domain.Release) ([]releaseIssue, error) {
	out := make([]releaseIssue, 0, len(rel.IssueIDs))
	for _, id := range rel.IssueIDs {
		is, err := a.Store.GetIssue(ctx, id)
		if err != nil {
			return nil, err
		}
		crs, err := a.Store.ListChangeRequestsForIssue(ctx, id)
		if err != nil {
			return nil, err
		}
		ri := releaseIssue{issue: is}
		for _, cr := range crs {
			if cr.Repo == rel.Repo && cr.Status == "merged" {
				ri.change = cr
			}
		}
		out = append(out, ri)
	}
	return out, nil
}

// previousVersion is the newest release of the repo with a version: its
// version and commit. Its zero value means none.
type previousVersion struct {
	version domain.Version
	sha     string
}

func (a *Activities) previousVersion(ctx context.Context, rel domain.Release) (*previousVersion, error) {
	all, err := a.Store.ListReleases(ctx, rel.ProjectID, 200)
	if err != nil {
		return nil, err
	}
	for _, r := range all { // newest first
		if r.Repo != rel.Repo || r.Version == "" || r.ID == rel.ID {
			continue
		}
		v, err := domain.ParseVersion(r.Version)
		if err != nil {
			return nil, err
		}
		return &previousVersion{version: v, sha: r.SHA}, nil
	}
	return nil, nil
}

// maxTaskSpec bounds each issue's spec in a release agent's task.
const maxTaskSpec = 1500

func writeReleaseIssues(b *strings.Builder, issues []releaseIssue) {
	if len(issues) == 0 {
		b.WriteString("\nThe release contains no issue Frankie merged; describe what changed from the commits.\n")
		return
	}
	b.WriteString("\nThe release contains these issues, in the order they merged:\n")
	for _, ri := range issues {
		is := ri.issue
		fmt.Fprintf(b, "\n### %s (%s): %s\n", is.Key, is.Type, strings.TrimSpace(is.Title))
		if ri.change.ProviderRef != "" {
			fmt.Fprintf(b, "Change request: %s", ri.change.ProviderRef)
			if ri.change.MergeSHA != "" {
				fmt.Fprintf(b, ", merged as %s (`git show %s`)", ri.change.MergeSHA, ri.change.MergeSHA)
			}
			b.WriteString("\n")
		}
		if spec := strings.TrimSpace(is.Spec); spec != "" {
			if len(spec) > maxTaskSpec {
				spec = clipText(spec, maxTaskSpec) + " [...]"
			}
			fmt.Fprintf(b, "\n%s\n", spec)
		}
	}
}

func notesTask(rel domain.Release, prev *previousVersion, issues []releaseIssue) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Write the release notes for commit %s of %s.\n", rel.SHA, rel.Repo)
	if prev != nil {
		fmt.Fprintf(&b, "The previous version is %s, at commit %s: this release is everything in `%s..%s`.\n", prev.version, prev.sha, prev.sha, rel.SHA)
	} else {
		fmt.Fprintf(&b, "This is the repository's first versioned release; it will be %s whatever the bump. Describe the issues below, not the whole history.\n", domain.FirstVersion)
	}
	writeReleaseIssues(&b, issues)
	return b.String()
}

func analysisTask(rel domain.Release, f ReleaseFailureReport, issues []releaseIssue) string {
	var b strings.Builder
	outcome := "was not rolled back"
	if f.RolledBack {
		outcome = "was rolled back"
	}
	fmt.Fprintf(&b, "Commit %s of %s failed verification on the %s stage and %s. Find which of its issues most likely broke it.\n", rel.SHA, rel.Repo, f.Stage, outcome)
	if s := strings.TrimSpace(f.Verification.Summary); s != "" {
		fmt.Fprintf(&b, "\nVerification: %s\n", s)
	}
	if len(f.Verification.Failures) > 0 {
		b.WriteString("\nFailing tests (the issue keys are the tests' tags):\n")
		for i, t := range f.Verification.Failures {
			if i == maxPreviewFailures {
				fmt.Fprintf(&b, "- and %d more\n", len(f.Verification.Failures)-i)
				break
			}
			fmt.Fprintf(&b, "- %s", t.Test)
			if len(t.IssueKeys) > 0 {
				fmt.Fprintf(&b, " [%s]", strings.Join(t.IssueKeys, ", "))
			}
			if t.Message != "" {
				fmt.Fprintf(&b, ": %s", clipText(t.Message, 500))
			}
			b.WriteString("\n")
		}
	}
	writeReleaseIssues(&b, issues)
	return b.String()
}

// clipText shortens s to at most n bytes at a rune boundary.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

// releasePrompt is the task of a release agent session: what
// PrepareReleaseSession wrote, then the contract reminder.
func releasePrompt(in IssueRunInput) string {
	return strings.TrimRight(in.Spec, "\n") + "\n\nYour final message must be only the JSON result your instructions describe."
}

// ReleaseNotesRequest records what the notes session returned.
type ReleaseNotesRequest struct {
	ReleaseID string
	// Result is the session's final message; Failed says why the session
	// failed, if it did.
	Result string `json:",omitempty"`
	Failed string `json:",omitempty"`
}

// RecordReleaseNotes records the release's bump and notes (D129): the
// agent's, when its answer meets the release-notes contract, otherwise the
// fallback (a patch when every issue is a bug, else a minor bump, and notes
// listing the issues). It returns who wrote them.
func (a *Activities) RecordReleaseNotes(ctx context.Context, req ReleaseNotesRequest) (domain.NotesSource, error) {
	relID, err := uuid.Parse(req.ReleaseID)
	if err != nil {
		return "", temporal.NewNonRetryableApplicationError("bad release id", "input", err)
	}
	if req.Failed == "" {
		n, err := domain.ParseReleaseNotes([]byte(strings.TrimSpace(req.Result)))
		if err == nil {
			notes := n.Notes
			if len(n.Breaking) > 0 {
				notes = strings.TrimRight(notes, "\n") + "\n\n### Breaking changes\n\n- " + strings.Join(n.Breaking, "\n- ") + "\n"
			}
			_, err = a.Store.SetReleaseNotes(ctx, relID, n.Bump, notes, domain.NotesByAgent)
			return domain.NotesByAgent, err
		}
		req.Failed = err.Error()
	}
	a.Log.Warn("release notes: the agent's answer is not usable, falling back", "release", req.ReleaseID, "reason", req.Failed)
	rel, err := a.Store.GetRelease(ctx, relID)
	if err != nil {
		return "", err
	}
	issues, err := a.releaseIssues(ctx, rel)
	if err != nil {
		return "", err
	}
	types := make([]domain.IssueType, 0, len(issues))
	var b strings.Builder
	for _, ri := range issues {
		types = append(types, ri.issue.Type)
		fmt.Fprintf(&b, "- %s: %s", ri.issue.Key, strings.TrimSpace(ri.issue.Title))
		if ri.change.ProviderRef != "" {
			fmt.Fprintf(&b, " (%s)", ri.change.ProviderRef)
		}
		b.WriteString("\n")
	}
	if len(issues) == 0 {
		fmt.Fprintf(&b, "Commit %s.\n", rel.SHA)
	}
	_, err = a.Store.SetReleaseNotes(ctx, relID, domain.FallbackBump(types), b.String(), domain.NotesByFallback)
	return domain.NotesByFallback, err
}

// PublishedVersion is the version a release got and where it was published;
// Error says why publishing failed, which leaves the release succeeded.
type PublishedVersion struct {
	Version string `json:",omitempty"`
	URL     string `json:",omitempty"`
	Error   string `json:",omitempty"`
}

// PublishVersion gives a succeeded release its version and publishes it as a
// provider release with its notes (D129): the newest version of the repo
// bumped as the release's notes propose, or v0.1.0 first. A release whose
// notes were never prepared gets the fallback first. A tag that names another
// commit, or another provider error that is not worth retrying, is recorded
// on the release instead. Idempotent: a release that has a version keeps it.
func (a *Activities) PublishVersion(ctx context.Context, releaseID string) (PublishedVersion, error) {
	relID, err := uuid.Parse(releaseID)
	if err != nil {
		return PublishedVersion{}, temporal.NewNonRetryableApplicationError("bad release id", "input", err)
	}
	rel, err := a.Store.GetRelease(ctx, relID)
	if err != nil {
		return PublishedVersion{}, err
	}
	if rel.Status != domain.ReleaseSucceeded {
		return PublishedVersion{}, temporal.NewNonRetryableApplicationError(fmt.Sprintf("release %s is %s, not succeeded", rel.ID, rel.Status), "input", nil)
	}
	if rel.Version != "" && (rel.ProviderReleaseURL != "" || rel.PublishError != "") {
		return PublishedVersion{Version: rel.Version, URL: rel.ProviderReleaseURL, Error: rel.PublishError}, nil
	}
	if rel.Bump == "" {
		if _, err := a.RecordReleaseNotes(ctx, ReleaseNotesRequest{ReleaseID: releaseID, Failed: "no notes were prepared"}); err != nil {
			return PublishedVersion{}, err
		}
		if rel, err = a.Store.GetRelease(ctx, relID); err != nil {
			return PublishedVersion{}, err
		}
	}
	version := rel.Version
	if version == "" {
		prev, err := a.previousVersion(ctx, rel)
		if err != nil {
			return PublishedVersion{}, err
		}
		next := domain.FirstVersion
		if prev != nil {
			next = prev.version.Next(rel.Bump)
		}
		version = next.String()
		// Recorded before publishing, so a retry publishes the same version.
		if _, err := a.Store.SetReleaseVersion(ctx, relID, next, "", ""); err != nil {
			return PublishedVersion{}, err
		}
	}
	url, err := a.GitHub.CreateRelease(ctx, rel.Repo, domain.NewProviderRelease{Tag: version, SHA: rel.SHA, Name: version, Body: rel.Notes})
	var se *github.StatusError
	publishErr := ""
	switch {
	case errors.Is(err, domain.ErrTagTaken):
		publishErr = err.Error()
	case errors.As(err, &se) && se.Code/100 == 4:
		publishErr = err.Error()
	case err != nil:
		return PublishedVersion{}, err
	}
	v, _ := domain.ParseVersion(version)
	if _, err := a.Store.SetReleaseVersion(ctx, relID, v, url, publishErr); err != nil {
		return PublishedVersion{}, err
	}
	return PublishedVersion{Version: version, URL: url, Error: publishErr}, nil
}

func (c ReleaseConfig) agentBudget() float64 { return cmp.Or(c.AgentBudgetUSD, 1) }
