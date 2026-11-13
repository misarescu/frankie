// Package intake turns labeled issues from an IssueSource into factory issues and
// starts their runs (D27). M1 polls GitHub; webhooks feed the same Poll path later.
package intake

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/connectors/github"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow/activities"
)

// IssueLister lists open issues carrying a label.
type IssueLister interface {
	ListOpenIssues(ctx context.Context, repo, label string) ([]github.Issue, error)
}

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
	Source   IssueLister
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

// Poll syncs labeled issues once. It is idempotent: an issue is created once per
// source URL, and a run is started only while the issue is ready.
func (in *Intake) Poll(ctx context.Context) error {
	var errs []error
	for _, p := range in.Projects {
		issues, err := in.Source.ListOpenIssues(ctx, p.Repo, in.Label)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, gi := range issues {
			if err := in.sync(ctx, p, gi); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (in *Intake) sync(ctx context.Context, p Project, gi github.Issue) error {
	is, err := in.Store.GetIssueBySourceRef(ctx, p.ID, gi.URL)
	if errors.Is(err, store.ErrNotFound) {
		// Labeling the issue is the approval to run it in M1 (spec approval comes with M5).
		is, err = in.Store.CreateIssue(ctx, store.NewIssue{
			ProjectID: p.ID, Type: domain.IssueTask, Title: gi.Title, Spec: gi.Body,
			Status: domain.IssueReady, SourceRef: gi.URL, Labels: gi.Labels, CreatedBy: "github:" + gi.Author,
		})
		if errors.Is(err, store.ErrConflict) {
			is, err = in.Store.GetIssueBySourceRef(ctx, p.ID, gi.URL)
		}
		if err == nil {
			in.Log.Info("issue taken in", "issue", is.Key, "source", gi.URL)
		}
	}
	if err != nil {
		return err
	}
	if is.Status != domain.IssueReady {
		return nil
	}
	return in.Starter.StartIssueRun(ctx, activities.IssueRunInput{
		IssueID: is.ID.String(), IssueKey: is.Key, Title: is.Title, Spec: is.Spec,
		AcceptanceCriteria: is.AcceptanceCriteria, SourceURL: gi.URL, SourceNumber: gi.Number,
		Repo: p.Repo, BaseBranch: p.BaseBranch,
	})
}
