package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// NewGateResult is the input for RecordGateResult.
type NewGateResult struct {
	StepRunID uuid.UUID
	Type      domain.GateType
	Policy    domain.GatePolicy
	Verdict   domain.GateVerdict
	Details   json.RawMessage // optional; must be a JSON object
}

// RecordGateResult stores the outcome of a gate StepRun and appends a
// gate_result event to the run's stream. Recording the same StepRun again
// replaces the verdict and details without a second event, so retried
// Activities stay idempotent.
func (s *Store) RecordGateResult(ctx context.Context, in NewGateResult) (domain.GateResult, error) {
	switch {
	case !in.Type.Valid():
		return domain.GateResult{}, fmt.Errorf("store: gate result: unknown type %q", in.Type)
	case !in.Policy.Valid():
		return domain.GateResult{}, fmt.Errorf("store: gate result: unknown policy %q", in.Policy)
	case !in.Verdict.Valid():
		return domain.GateResult{}, fmt.Errorf("store: gate result: unknown verdict %q", in.Verdict)
	}
	details := in.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(details, &obj); err != nil {
		return domain.GateResult{}, fmt.Errorf("store: gate result: details must be a JSON object: %w", err)
	}

	var out domain.GateResult
	err := s.inTx(ctx, func(q *db.Queries) error {
		step, err := q.GetStepRun(ctx, in.StepRunID)
		if err != nil {
			return mapErr("gate result step run", err)
		}
		id := domain.NewID()
		row, err := q.RecordGateResult(ctx, db.RecordGateResultParams{
			ID: id, StepRunID: in.StepRunID, GateType: string(in.Type),
			Policy: string(in.Policy), Verdict: string(in.Verdict), Details: details,
		})
		if err != nil {
			return mapErr("record gate result", err)
		}
		out = gateResultFromRow(row)
		if row.ID != id {
			return nil // already recorded by an earlier attempt, with its event
		}
		payload, err := json.Marshal(map[string]any{
			"gate": step.StepID, "type": in.Type, "policy": in.Policy, "verdict": in.Verdict,
			"commit": step.CommitSha, "details": details,
		})
		if err != nil {
			return fmt.Errorf("store: gate result event: %w", err)
		}
		if _, err := q.AppendEvent(ctx, db.AppendEventParams{
			ID: domain.NewID(), RunID: &step.RunID, StepRunID: &step.ID, Ts: time.Now().UTC(),
			Type: string(domain.EventGateResult), Payload: payload,
		}); err != nil {
			return mapErr("append gate result event", err)
		}
		return notifyEvent(ctx, q, &step.RunID)
	})
	return out, err
}

// ListGateResultsForRun returns a Run's gate results, oldest first.
func (s *Store) ListGateResultsForRun(ctx context.Context, runID uuid.UUID) ([]domain.GateResult, error) {
	rows, err := s.q.ListGateResultsForRun(ctx, runID)
	if err != nil {
		return nil, mapErr("list gate results", err)
	}
	out := make([]domain.GateResult, 0, len(rows))
	for _, r := range rows {
		out = append(out, gateResultFromRow(r))
	}
	return out, nil
}

func gateResultFromRow(r db.GateResult) domain.GateResult {
	return domain.GateResult{
		ID: r.ID, StepRunID: r.StepRunID, Type: domain.GateType(r.GateType), Policy: domain.GatePolicy(r.Policy),
		Verdict: domain.GateVerdict(r.Verdict), Details: r.Details, CreatedAt: r.CreatedAt,
	}
}
