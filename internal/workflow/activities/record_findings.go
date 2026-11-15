package activities

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
)

// FindingsRequest is an investigating step's final message to check.
type FindingsRequest struct {
	RunID     string
	StepRunID string
	Result    string // the agent's final message
}

// FindingsResult is the step's findings, or why its message holds none.
type FindingsResult struct {
	Findings domain.BugFindings
	Error    string `json:",omitempty"`
}

// RecordFindings parses an investigating step's final message against the
// bug-findings contract (D94) and records the findings, or the reason they
// are not valid, on the step's stream. Like a review verdict, the JSON object
// may come in a code fence or after a sentence.
func (a *Activities) RecordFindings(ctx context.Context, req FindingsRequest) (FindingsResult, error) {
	runID, err1 := uuid.Parse(req.RunID)
	stepID, err2 := uuid.Parse(req.StepRunID)
	if err := errors.Join(err1, err2); err != nil {
		return FindingsResult{}, temporal.NewNonRetryableApplicationError("bad id", "input", err)
	}
	var out FindingsResult
	s := strings.TrimSpace(req.Result)
	start, end := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		out.Error = "the final message holds no JSON findings"
	} else if f, err := domain.ParseBugFindings([]byte(s[start : end+1])); err != nil {
		out.Error = err.Error()
	} else {
		out.Findings = f
	}
	payload, err := json.Marshal(map[string]any{"findings": out.Findings, "error": out.Error})
	if err != nil {
		return FindingsResult{}, err
	}
	if out.Error != "" {
		payload, _ = json.Marshal(map[string]string{"error": out.Error})
	}
	if _, err := a.Store.AppendEvent(ctx, store.NewEvent{RunID: &runID, StepRunID: &stepID, Ts: time.Now(),
		Type: domain.EventFindings, Payload: payload}); err != nil {
		return FindingsResult{}, err
	}
	return out, nil
}
