// Package intake syncs issues from an IssueSource into the factory's store and
// starts their runs (D27). It polls; webhooks feed the same Poll path later.
// Since D60 it syncs whole trees: the labeled issues, their sub-issues and the
// issues they are blocked by, with parents and dependencies.
package intake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// Starter starts an issue's run; starting one that is already running is a no-op.
type Starter interface {
	StartIssueRun(ctx context.Context, in activities.IssueRunInput) error
}

// Project is a repo the factory takes issues from.
type Project struct {
	ID         uuid.UUID
	Repo       string
	BaseBranch string
}

// Intake polls the issue source.
type Intake struct {
	Store    *store.Store
	Source   IssueSource
	Starter  Starter
	Projects []Project
	Label    string
	Interval time.Duration
	Log      *slog.Logger
}

// Run polls until ctx ends.
func (in *Intake) Run(ctx context.Context) {
	for {
		if err := in.Poll(ctx); err != nil {
			in.Log.Warn("intake poll", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(in.Interval):
		}
	}
}

// Poll syncs every project's tree once and starts its ready issues. It is
// idempotent: an issue is created once per source ref, and a run is started only
// while the issue is ready.
func (in *Intake) Poll(ctx context.Context) error {
	var errs []error
	for _, p := range in.Projects {
		if err := in.syncProject(ctx, p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (in *Intake) syncProject(ctx context.Context, p Project) error {
	t, err := collectTree(ctx, in.Source, p.Repo, in.Label)
	if err != nil {
		return err
	}
	src := make([]store.SourceIssue, 0, len(t.order))
	for _, ref := range t.order {
		n := t.nodes[ref]
		typ := t.issueType(n)
		src = append(src, store.SourceIssue{Ref: ref, Type: typ, Title: n.issue.Title, Spec: n.issue.Body,
			Labels: n.issue.Labels, CreatedBy: "github:" + n.issue.Author, ParentRef: n.parent,
			BlockedBy: n.blockedBy, Status: t.newStatus(n, typ)})
	}
	res, err := in.Store.SyncIssues(ctx, p.ID, src)
	if err != nil {
		return fmt.Errorf("sync %s: %w", p.Repo, err)
	}
	for _, key := range res.Created {
		in.Log.Info("issue taken in", "issue", key, "project", p.Repo)
	}
	for _, w := range res.Warnings {
		in.Log.Warn("issue sync", "project", p.Repo, "problem", w)
	}

	var errs []error
	for _, ref := range t.order {
		is := res.Issues[ref]
		if is.Status != domain.IssueReady {
			continue
		}
		n := t.nodes[ref].issue
		if err := in.Starter.StartIssueRun(ctx, activities.IssueRunInput{
			IssueID: is.ID.String(), IssueKey: is.Key, Title: is.Title, Spec: is.Spec,
			AcceptanceCriteria: is.AcceptanceCriteria, SourceURL: ref, SourceNumber: n.Number,
			Repo: p.Repo, BaseBranch: p.BaseBranch,
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
