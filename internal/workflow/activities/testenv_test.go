package activities

import (
	"io"
	"log/slog"
	"testing"

	"go.temporal.io/sdk/testsuite"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/store/storetest"
)

// Shared fixtures for the Activity tests: a store with one ready issue, and a
// Temporal test environment that runs Activities with a real activity context.

func activityEnv(a *Activities) *testsuite.TestActivityEnvironment {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestActivityEnvironment()
	env.RegisterActivity(a)
	return env
}

func readyIssue(t *testing.T) (*store.Store, IssueRunInput) {
	t.Helper()
	st := storetest.New(t)
	p, err := st.CreateProject(t.Context(), "misarescu/Goodreads-Clone", "gc")
	if err != nil {
		t.Fatal(err)
	}
	is, err := st.CreateIssue(t.Context(), store.NewIssue{ProjectID: p.ID, Type: domain.IssueTask, Title: "Add search",
		Status: domain.IssueReady, CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return st, IssueRunInput{IssueID: is.ID.String(), IssueKey: is.Key, Title: is.Title, Repo: "misarescu/Goodreads-Clone", BaseBranch: "main", SourceNumber: 2}
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
