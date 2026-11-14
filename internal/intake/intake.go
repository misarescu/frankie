// Package intake syncs issues from an IssueSource into the factory's store (D27).
// It polls; webhooks feed the same Poll path later. Since D60 it syncs whole
// trees: the labeled issues, their sub-issues and the issues they are blocked
// by, with parents and dependencies. Since D62 it starts nothing: the scheduler
// starts issues someone asked to start.
package intake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/config"
	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// Project is a repo the factory takes issues from.
type Project struct {
	ID         uuid.UUID
	Repo       string
	BaseBranch string
}

// ConfigReader reads a file of a repo at a branch: intake reads the project's
// .factory/config.yaml with it.
type ConfigReader interface {
	FileAt(ctx context.Context, repo, path, ref string) (content []byte, found bool, err error)
}

// Intake polls the issue source.
type Intake struct {
	Store  *store.Store
	Source IssueSource
	// Config, when set, syncs each project's settings from its repo (D64).
	Config   ConfigReader
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

// Poll syncs every project's tree once. It is idempotent: an issue is created
// once per source ref.
func (in *Intake) Poll(ctx context.Context) error {
	var errs []error
	for _, p := range in.Projects {
		if err := in.syncProject(ctx, p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// syncConfig copies the project's settings from .factory/config.yaml on its base
// branch into the store (docs/spec/02: the repo copy is authoritative). A file
// that cannot be read or parsed leaves the stored settings as they are; a run
// started from it reports the problem.
func (in *Intake) syncConfig(ctx context.Context, p Project) {
	raw, found, err := in.Config.FileAt(ctx, p.Repo, config.ProjectConfigPath, p.BaseBranch)
	if err != nil {
		in.Log.Warn("read project config", "project", p.Repo, "error", err)
		return
	}
	var pc config.Project
	if found {
		if pc, err = config.ParseProject(raw, math.MaxInt); err != nil {
			in.Log.Warn("project config", "project", p.Repo, "error", err)
			return
		}
	}
	if err := in.Store.SetProjectSpecApproval(ctx, p.ID, pc.RequiresSpecApproval()); err != nil {
		in.Log.Warn("store project config", "project", p.Repo, "error", err)
	}
}

func (in *Intake) syncProject(ctx context.Context, p Project) error {
	if in.Config != nil {
		in.syncConfig(ctx, p)
	}
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
			BlockedBy: n.blockedBy, Status: domain.IssueDraft, FileHints: domain.ExtractFileHints(n.issue.Body)})
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

	return nil
}
