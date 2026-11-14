package activities

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// Budgets (D53): a session's virtual key is capped at the step's budget, or
// at what is left of the issue's if that is less; the gateway refuses the
// key's requests once it has spent its cap. When the session ends, the key's
// spend and the harness's token totals are added to the StepRun, so steps,
// runs and issues have their usage.

// spendSettleChecks bounds how often a session's spend is read while the
// gateway is still writing it.
const spendSettleChecks = 5

// sessionBudget is the cap for a session's key. exhausted is set, with the
// key's cap zero, when the issue's budget is used up: the session must not
// start.
func (a *Activities) sessionBudget(ctx context.Context, in IssueRunInput, run StartRunResult, set sessionSettings) (budget float64, exhausted string, err error) {
	issueBudget := a.Agent.IssueBudgetUSD
	if run.Step != nil {
		issueBudget = cmp.Or(run.Step.IssueBudgetUSD, issueBudget)
	}
	if issueBudget <= 0 {
		return set.budget, "", nil
	}
	issueID, err := uuid.Parse(in.IssueID)
	if err != nil {
		return 0, "", fmt.Errorf("issue id %q: %w", in.IssueID, err)
	}
	spent, err := a.Store.IssueSpend(ctx, issueID)
	if err != nil {
		return 0, "", err
	}
	left := issueBudget - spent
	if left <= 0.005 { // a cent's fraction is not a session
		return 0, fmt.Sprintf("the issue's model budget of $%.2f is used up ($%.2f spent)", issueBudget, spent), nil
	}
	return min(set.budget, left), "", nil
}

// tokenTotals are the harness's session totals from its last token_usage event.
type tokenTotals struct {
	Input  int64 `json:"input_tokens"`
	Output int64 `json:"output_tokens"`
}

// observe keeps the totals of a token_usage event's payload.
func (t *tokenTotals) observe(payload []byte) {
	var u tokenTotals
	if json.Unmarshal(payload, &u) == nil {
		*t = u
	}
}

// keySpend reads what a session's key spent once the gateway has written it:
// it reads until two reads agree, a few seconds apart.
func (a *Activities) keySpend(ctx context.Context, alias string) (float64, error) {
	settle := cmp.Or(a.SpendSettle, 3*time.Second)
	last, err := a.Gateway.KeySpend(ctx, alias)
	if err != nil {
		return 0, err
	}
	for range spendSettleChecks {
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(settle):
		}
		spend, err := a.Gateway.KeySpend(ctx, alias)
		if err != nil {
			return 0, err
		}
		if spend == last {
			return spend, nil
		}
		last = spend
	}
	return last, nil
}

// recordUsage adds a finished session's spend and tokens to its StepRun and
// returns the spend. A spend the gateway cannot report is recorded as zero
// and logged: the session's outcome stands either way.
func (a *Activities) recordUsage(ctx context.Context, stepRunID uuid.UUID, alias string, tokens tokenTotals) float64 {
	spend, err := a.keySpend(ctx, alias)
	if err != nil {
		a.Log.Warn("read session spend", "step_run", stepRunID, "key", alias, "error", err)
	}
	u := domain.Usage{CostUSD: spend, InputTokens: tokens.Input, OutputTokens: tokens.Output}
	if _, err := a.Store.AddStepRunUsage(ctx, stepRunID, u); err != nil {
		a.Log.Warn("record session usage", "step_run", stepRunID, "error", err)
	}
	return spend
}

// budgetReason explains a failed session whose key spent its whole cap: the
// gateway refused its last requests, which the harness reports only as
// errors.
func budgetReason(status string, spend, keyBudget float64) string {
	if status == "succeeded" || keyBudget <= 0 || spend < keyBudget*0.98 {
		return ""
	}
	return fmt.Sprintf("model budget used up: the session spent $%.2f of its $%.2f cap", spend, keyBudget)
}
