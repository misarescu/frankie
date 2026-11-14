package activities

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/gateway"
)

// fakeSpend serves /key/list for one key alias whose recorded spend moves
// through spends, one value per read, then stays at the last.
func fakeSpend(t *testing.T, alias string, spends ...float64) *gateway.Client {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/key/list" || r.URL.Query().Get("key_alias") != alias {
			_, _ = w.Write([]byte(`{"keys":[]}`))
			return
		}
		mu.Lock()
		s := spends[0]
		if len(spends) > 1 {
			spends = spends[1:]
		}
		mu.Unlock()
		_, _ = w.Write([]byte(`{"keys":[{"key_alias":"` + alias + `","spend":` + strconv.FormatFloat(s, 'f', -1, 64) + `}]}`))
	}))
	t.Cleanup(srv.Close)
	c, err := gateway.New(srv.URL, "sk-master")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// D53: a session's key is capped at the step's budget, or at what is left of
// the issue's if that is less; an issue that has spent its budget starts no
// session.
func TestSessionBudget(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog(), Agent: AgentConfig{IssueBudgetUSD: 10}}
	run := startedRun(t, a, in)
	set := sessionSettings{budget: 2}
	stepRun := func(id string) uuid.UUID {
		t.Helper()
		v, err := activityEnv(a).ExecuteActivity(a.StartStep, StepStart{RunID: run.RunID, StepID: id, Attempt: 1, Kind: domain.StepAgent})
		if err != nil {
			t.Fatal(err)
		}
		var s string
		_ = v.Get(&s)
		return uuid.MustParse(s)
	}
	check := func(name string, r StartRunResult, wantBudget float64, wantExhausted bool) {
		t.Helper()
		budget, exhausted, err := a.sessionBudget(t.Context(), in, r, set)
		if err != nil {
			t.Fatal(err)
		}
		if budget != wantBudget || (exhausted != "") != wantExhausted {
			t.Errorf("%s: budget %v, exhausted %q", name, budget, exhausted)
		}
	}

	check("nothing spent", run, 2, false)
	if _, err := st.AddStepRunUsage(t.Context(), stepRun("implement"), domain.Usage{CostUSD: 9}); err != nil {
		t.Fatal(err)
	}
	check("a dollar left", run, 1, false)
	project := run
	project.Step = &AgentStep{StepID: "review", IssueBudgetUSD: 50}
	check("the project's budget", project, 2, false)
	if _, err := st.AddStepRunUsage(t.Context(), stepRun("review"), domain.Usage{CostUSD: 1}); err != nil {
		t.Fatal(err)
	}
	check("used up", run, 0, true)
	_, exhausted, _ := a.sessionBudget(t.Context(), in, run, set)
	if !strings.Contains(exhausted, "$10.00 is used up ($10.00 spent)") {
		t.Errorf("reason = %q", exhausted)
	}
	unlimited := &Activities{Store: st, Log: quietLog()}
	if b, ex, err := unlimited.sessionBudget(t.Context(), in, run, set); err != nil || b != 2 || ex != "" {
		t.Errorf("no issue budget: %v %q %v", b, ex, err)
	}
}

func TestRecordUsageWaitsForTheGatewaysSpend(t *testing.T) {
	st, in := readyIssue(t)
	a := &Activities{Store: st, Log: quietLog(), SpendSettle: time.Millisecond,
		Gateway: fakeSpend(t, "run-1-step-2-attempt-1", 0, 0.3, 0.42, 0.42)}
	run := startedRun(t, a, in)
	v, err := activityEnv(a).ExecuteActivity(a.StartStep, StepStart{RunID: run.RunID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	_ = v.Get(&id)
	var tokens tokenTotals
	tokens.observe([]byte(`{"input_tokens":1200,"output_tokens":80,"cost_usd":0,"turns":2}`))
	tokens.observe([]byte(`not json`)) // ignored
	if spend := a.recordUsage(t.Context(), uuid.MustParse(id), "run-1-step-2-attempt-1", tokens); spend != 0.42 {
		t.Errorf("spend = %v, want the settled 0.42", spend)
	}
	got, err := st.GetStepRun(t.Context(), uuid.MustParse(id))
	if err != nil || got.Usage != (domain.Usage{CostUSD: 0.42, InputTokens: 1200, OutputTokens: 80}) {
		t.Errorf("usage = %+v, %v", got.Usage, err)
	}
	// A key the gateway does not know records the tokens with no cost.
	if spend := a.recordUsage(t.Context(), uuid.MustParse(id), "run-gone", tokenTotals{Input: 1}); spend != 0 {
		t.Errorf("unknown key spend = %v", spend)
	}
}

func TestBudgetReason(t *testing.T) {
	tests := []struct {
		status           string
		spend, keyBudget float64
		want             bool
	}{
		{"failed", 2, 2, true},
		{"failed", 1.99, 2, true},
		{"failed", 0.5, 2, false},
		{"succeeded", 2, 2, false},
		{"failed", 0, 0, false},
	}
	for _, tt := range tests {
		got := budgetReason(tt.status, tt.spend, tt.keyBudget)
		if (got != "") != tt.want {
			t.Errorf("budgetReason(%s, %v, %v) = %q", tt.status, tt.spend, tt.keyBudget, got)
		}
	}
	if got := budgetReason("failed", 2, 2); !strings.Contains(got, "$2.00 of its $2.00 cap") {
		t.Errorf("reason = %q", got)
	}
}
