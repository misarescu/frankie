package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// NewRun is the input for CreateRun.
type NewRun struct {
	IssueID            uuid.UUID
	WorkflowSnapshot   json.RawMessage
	WorkflowVersion    string
	TemporalWorkflowID string
	TemporalRunID      string // optional; unique when set
}

// CreateRun inserts a pending Run. It returns ErrConflict if the issue already has an
// active (pending or running) Run, or if the Temporal run ID is taken.
func (s *Store) CreateRun(ctx context.Context, in NewRun) (domain.Run, error) {
	switch {
	case !json.Valid(in.WorkflowSnapshot):
		return domain.Run{}, errors.New("store: create run: workflow snapshot is not valid JSON")
	case in.WorkflowVersion == "":
		return domain.Run{}, errors.New("store: create run: empty workflow version")
	case in.TemporalWorkflowID == "":
		return domain.Run{}, errors.New("store: create run: empty temporal workflow id")
	}
	row, err := s.q.CreateRun(ctx, db.CreateRunParams{
		ID:                 domain.NewID(),
		IssueID:            in.IssueID,
		WorkflowSnapshot:   in.WorkflowSnapshot,
		WorkflowVersion:    in.WorkflowVersion,
		TemporalWorkflowID: in.TemporalWorkflowID,
		TemporalRunID:      nonEmpty(in.TemporalRunID),
	})
	if err != nil {
		return domain.Run{}, mapErr("create run", err)
	}
	return runFromRow(row)
}

// GetRun returns a Run by ID.
func (s *Store) GetRun(ctx context.Context, id uuid.UUID) (domain.Run, error) {
	row, err := s.q.GetRun(ctx, id)
	if err != nil {
		return domain.Run{}, mapErr("get run", err)
	}
	return runFromRow(row)
}

// GetRunByTemporalRunID returns the Run created by one Temporal execution.
func (s *Store) GetRunByTemporalRunID(ctx context.Context, temporalRunID string) (domain.Run, error) {
	row, err := s.q.GetRunByTemporalRunID(ctx, &temporalRunID)
	if err != nil {
		return domain.Run{}, mapErr("get run by temporal run id", err)
	}
	return runFromRow(row)
}

// GetActiveRunForIssue returns the issue's pending or running Run, or ErrNotFound.
func (s *Store) GetActiveRunForIssue(ctx context.Context, issueID uuid.UUID) (domain.Run, error) {
	row, err := s.q.GetActiveRunForIssue(ctx, issueID)
	if err != nil {
		return domain.Run{}, mapErr("get active run", err)
	}
	return runFromRow(row)
}

// TransitionRun moves a Run between statuses with the same contract as TransitionIssue.
// Entering running stamps started_at; entering a terminal status stamps finished_at.
func (s *Store) TransitionRun(ctx context.Context, id uuid.UUID, from, to domain.ExecStatus) (domain.Run, error) {
	if !domain.CanTransitionExec(from, to) {
		return domain.Run{}, fmt.Errorf("store: run %s: %w: %s -> %s", id, domain.ErrInvalidTransition, from, to)
	}
	var out domain.Run
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := q.TransitionRun(ctx, db.TransitionRunParams{ID: id, FromStatus: string(from), ToStatus: string(to)})
		if err != nil {
			return mapErr("transition run", err)
		}
		if out, err = runFromRow(row); err != nil {
			return err
		}
		return appendStateChange(ctx, q, &id, nil, domain.StateChange{Entity: "run", ID: id, From: string(from), To: string(to)})
	})
	if errors.Is(err, ErrNotFound) {
		current, getErr := s.GetRun(ctx, id)
		if getErr != nil {
			return domain.Run{}, getErr
		}
		if current.Status == to {
			return current, nil
		}
		return domain.Run{}, fmt.Errorf("store: run %s: %w: expected %s, found %s", id, ErrConflict, from, current.Status)
	}
	return out, err
}

// SetRunLoopCount records that a gate has looped back loops times in this Run.
// Setting the count (not incrementing it) keeps a retried Activity idempotent.
func (s *Store) SetRunLoopCount(ctx context.Context, id uuid.UUID, gateStepID string, loops int) (domain.Run, error) {
	if gateStepID == "" || loops < 0 {
		return domain.Run{}, fmt.Errorf("store: run %s: invalid loop count %q=%d", id, gateStepID, loops)
	}
	row, err := s.q.SetRunLoopCount(ctx, db.SetRunLoopCountParams{ID: id, StepID: gateStepID, Loops: int32(loops)}) //nolint:gosec // bounded by MaxLoopsLimit
	if err != nil {
		return domain.Run{}, mapErr("set run loop count", err)
	}
	return runFromRow(row)
}

// NewStepRun is the input for CreateStepRun.
type NewStepRun struct {
	RunID   uuid.UUID
	StepID  string
	Attempt int
	Kind    domain.StepKind
}

// CreateStepRun inserts a pending StepRun. A repeated (run, step, attempt) returns ErrConflict.
func (s *Store) CreateStepRun(ctx context.Context, in NewStepRun) (domain.StepRun, error) {
	switch {
	case !in.Kind.Valid():
		return domain.StepRun{}, fmt.Errorf("store: create step run: unknown kind %q", in.Kind)
	case in.StepID == "":
		return domain.StepRun{}, errors.New("store: create step run: empty step id")
	case in.Attempt < 1:
		return domain.StepRun{}, fmt.Errorf("store: create step run: attempt %d < 1", in.Attempt)
	}
	row, err := s.q.CreateStepRun(ctx, db.CreateStepRunParams{
		ID:      domain.NewID(),
		RunID:   in.RunID,
		StepID:  in.StepID,
		Attempt: int32(in.Attempt),
		Kind:    string(in.Kind),
	})
	if err != nil {
		return domain.StepRun{}, mapErr("create step run", err)
	}
	return stepRunFromRow(row), nil
}

// GetStepRun returns a StepRun by ID.
func (s *Store) GetStepRun(ctx context.Context, id uuid.UUID) (domain.StepRun, error) {
	row, err := s.q.GetStepRun(ctx, id)
	if err != nil {
		return domain.StepRun{}, mapErr("get step run", err)
	}
	return stepRunFromRow(row), nil
}

// ListStepRuns returns a Run's StepRuns in creation order.
func (s *Store) ListStepRuns(ctx context.Context, runID uuid.UUID) ([]domain.StepRun, error) {
	rows, err := s.q.ListStepRuns(ctx, runID)
	if err != nil {
		return nil, mapErr("list step runs", err)
	}
	out := make([]domain.StepRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, stepRunFromRow(r))
	}
	return out, nil
}

// TransitionStepRun moves a StepRun between statuses and records a state_change event on
// its Run's stream (FR-WF-7), with the same contract as TransitionIssue.
func (s *Store) TransitionStepRun(ctx context.Context, id uuid.UUID, from, to domain.ExecStatus) (domain.StepRun, error) {
	if !domain.CanTransitionExec(from, to) {
		return domain.StepRun{}, fmt.Errorf("store: step run %s: %w: %s -> %s", id, domain.ErrInvalidTransition, from, to)
	}
	var out domain.StepRun
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := q.TransitionStepRun(ctx, db.TransitionStepRunParams{ID: id, FromStatus: string(from), ToStatus: string(to)})
		if err != nil {
			return mapErr("transition step run", err)
		}
		out = stepRunFromRow(row)
		return appendStateChange(ctx, q, &row.RunID, &id, domain.StateChange{Entity: "step_run", ID: id, From: string(from), To: string(to)})
	})
	if errors.Is(err, ErrNotFound) {
		current, getErr := s.GetStepRun(ctx, id)
		if getErr != nil {
			return domain.StepRun{}, getErr
		}
		if current.Status == to {
			return current, nil
		}
		return domain.StepRun{}, fmt.Errorf("store: step run %s: %w: expected %s, found %s", id, ErrConflict, from, current.Status)
	}
	return out, err
}

// SetStepRunPod records the pod executing a StepRun.
func (s *Store) SetStepRunPod(ctx context.Context, id uuid.UUID, podName string) (domain.StepRun, error) {
	row, err := s.q.SetStepRunPod(ctx, db.SetStepRunPodParams{ID: id, PodName: &podName})
	if err != nil {
		return domain.StepRun{}, mapErr("set step run pod", err)
	}
	return stepRunFromRow(row), nil
}

// SetStepRunCommit records the commit a StepRun pushed.
func (s *Store) SetStepRunCommit(ctx context.Context, id uuid.UUID, sha string) (domain.StepRun, error) {
	row, err := s.q.SetStepRunCommit(ctx, db.SetStepRunCommitParams{ID: id, CommitSha: &sha})
	if err != nil {
		return domain.StepRun{}, mapErr("set step run commit", err)
	}
	return stepRunFromRow(row), nil
}

// AddStepRunUsage adds a session's model usage to its StepRun; a retried
// session's attempts each add their own.
func (s *Store) AddStepRunUsage(ctx context.Context, id uuid.UUID, u domain.Usage) (domain.StepRun, error) {
	row, err := s.q.AddStepRunUsage(ctx, db.AddStepRunUsageParams{ID: id, CostUsd: u.CostUSD, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens})
	if err != nil {
		return domain.StepRun{}, mapErr("add step run usage", err)
	}
	return stepRunFromRow(row), nil
}

// IssueSpend is what all runs of an issue have spent on models so far.
func (s *Store) IssueSpend(ctx context.Context, issueID uuid.UUID) (float64, error) {
	spent, err := s.q.IssueSpend(ctx, issueID)
	if err != nil {
		return 0, mapErr("issue spend", err)
	}
	return spent, nil
}

func runFromRow(r db.Run) (domain.Run, error) {
	loops := map[string]int{}
	if err := json.Unmarshal(r.LoopCounts, &loops); err != nil {
		return domain.Run{}, fmt.Errorf("store: run %s: decode loop_counts: %w", r.ID, err)
	}
	return domain.Run{
		ID:                 r.ID,
		IssueID:            r.IssueID,
		WorkflowSnapshot:   r.WorkflowSnapshot,
		WorkflowVersion:    r.WorkflowVersion,
		TemporalWorkflowID: r.TemporalWorkflowID,
		TemporalRunID:      deref(r.TemporalRunID),
		Status:             domain.ExecStatus(r.Status),
		LoopCounts:         loops,
		StartedAt:          r.StartedAt,
		FinishedAt:         r.FinishedAt,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}, nil
}

func stepRunFromRow(r db.StepRun) domain.StepRun {
	return domain.StepRun{
		ID:         r.ID,
		RunID:      r.RunID,
		StepID:     r.StepID,
		Attempt:    int(r.Attempt),
		Kind:       domain.StepKind(r.Kind),
		Status:     domain.ExecStatus(r.Status),
		PodName:    deref(r.PodName),
		CommitSHA:  deref(r.CommitSha),
		Usage:      domain.Usage{CostUSD: r.CostUsd, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens},
		StartedAt:  r.StartedAt,
		FinishedAt: r.FinishedAt,
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

// RunSummary is a run with its issue, for listings.
type RunSummary struct {
	ID         uuid.UUID
	Status     domain.ExecStatus
	IssueKey   string
	IssueTitle string
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	// CostUSD is what the run's sessions spent on models (D53).
	CostUSD float64
}

// ListRecentRuns returns the newest runs first.
func (s *Store) ListRecentRuns(ctx context.Context, limit int) ([]RunSummary, error) {
	rows, err := s.q.ListRecentRuns(ctx, int32(min(max(limit, 1), 500)))
	if err != nil {
		return nil, mapErr("list recent runs", err)
	}
	out := make([]RunSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, RunSummary{ID: r.ID, Status: domain.ExecStatus(r.Status), IssueKey: r.IssueKey, IssueTitle: r.IssueTitle,
			CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, CostUSD: r.CostUsd})
	}
	return out, nil
}
