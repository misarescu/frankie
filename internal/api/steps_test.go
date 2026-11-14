package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow"
)

// stepIn adds a step execution to the test run in the given status.
func stepIn(t *testing.T, st *store.Store, run domain.Run, stepID string, kind domain.StepKind, status domain.ExecStatus) domain.StepRun {
	t.Helper()
	step, err := st.CreateStepRun(t.Context(), store.NewStepRun{RunID: run.ID, StepID: stepID, Attempt: 1, Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	from := domain.ExecPending
	for _, to := range []domain.ExecStatus{domain.ExecRunning, status} {
		if to == from {
			continue
		}
		if step, err = st.TransitionStepRun(t.Context(), step.ID, from, to); err != nil {
			t.Fatal(err)
		}
		from = to
	}
	return step
}

func postJSON(t *testing.T, url, body string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestListSteps(t *testing.T) {
	st, srv, run, _ := testServer(t)
	impl := stepIn(t, st, run, "implement", domain.StepAgent, domain.ExecSucceeded)
	if _, err := st.AddStepRunUsage(t.Context(), impl.ID, domain.Usage{CostUSD: 0.42, InputTokens: 1200, OutputTokens: 80}); err != nil {
		t.Fatal(err)
	}
	stepIn(t, st, run, "checks", domain.StepGate, domain.ExecRunning)
	var got []stepJSON
	getJSON(t, srv.URL+"/api/runs/"+run.ID.String()+"/steps", &got)
	if len(got) != 2 || got[0].StepID != "implement" || got[0].Status != "succeeded" || got[1].Kind != "gate" || got[1].Status != "running" ||
		got[0].CostUSD != 0.42 || got[0].InputTokens != 1200 || got[0].OutputTokens != 80 {
		t.Errorf("steps = %+v", got)
	}
	if code := getJSON(t, srv.URL+"/api/runs/0198f2a4-0000-7000-8000-000000000000/steps", nil); code != http.StatusNotFound {
		t.Errorf("unknown run: %d", code)
	}
}

// docs/spec/04, "Human intervention": a steer or abort of a running agent
// step becomes a signal to the run's workflow, naming the step.
func TestSteerAndAbortSignalTheWorkflow(t *testing.T) {
	sig := &fakeSignaler{}
	st, srv, run, _ := testServerWith(t, sig)
	step := stepIn(t, st, run, "implement", domain.StepAgent, domain.ExecRunning)
	base := srv.URL + "/api/steps/" + step.ID.String()

	if code := postJSON(t, base+"/steer", `{"text":" use the helper ","by":"ana"}`); code != http.StatusAccepted {
		t.Fatalf("steer: %d", code)
	}
	if code := postJSON(t, base+"/abort", `{"reason":"wrong approach"}`); code != http.StatusAccepted {
		t.Fatalf("abort: %d", code)
	}
	want := []signal{
		{run.TemporalWorkflowID, run.TemporalRunID, workflow.SteerSignal, workflow.StepIntervention{StepRunID: step.ID.String(), By: "ana", Text: "use the helper"}},
		{run.TemporalWorkflowID, run.TemporalRunID, workflow.AbortSignal, workflow.StepIntervention{StepRunID: step.ID.String(), By: "web", Text: "wrong approach"}},
	}
	if len(sig.sent) != len(want) {
		t.Fatalf("signals = %+v", sig.sent)
	}
	for i := range want {
		if sig.sent[i] != want[i] {
			t.Errorf("signal %d = %+v, want %+v", i, sig.sent[i], want[i])
		}
	}
}

func TestInterventionRejects(t *testing.T) {
	sig := &fakeSignaler{}
	st, srv, run, _ := testServerWith(t, sig)
	running := stepIn(t, st, run, "implement", domain.StepAgent, domain.ExecRunning)
	done := stepIn(t, st, run, "review", domain.StepAgent, domain.ExecSucceeded)
	gate := stepIn(t, st, run, "checks", domain.StepGate, domain.ExecRunning)
	url := func(id, action string) string { return srv.URL + "/api/steps/" + id + "/" + action }
	tests := []struct {
		name, url, body string
		want            int
	}{
		{"no text", url(running.ID.String(), "steer"), `{"text":"  "}`, http.StatusBadRequest},
		{"abort without reason", url(running.ID.String(), "abort"), `{"text":"x"}`, http.StatusBadRequest},
		{"unknown field", url(running.ID.String(), "steer"), `{"text":"x","force":true}`, http.StatusBadRequest},
		{"too long", url(running.ID.String(), "steer"), `{"text":"` + strings.Repeat("x", maxInterventionText+1) + `"}`, http.StatusBadRequest},
		{"bad id", url("nope", "steer"), `{"text":"x"}`, http.StatusBadRequest},
		{"unknown step", url("0198f2a4-0000-7000-8000-000000000000", "steer"), `{"text":"x"}`, http.StatusNotFound},
		{"finished step", url(done.ID.String(), "steer"), `{"text":"x"}`, http.StatusConflict},
		{"gate step", url(gate.ID.String(), "abort"), `{"reason":"x"}`, http.StatusConflict},
	}
	for _, tt := range tests {
		if code := postJSON(t, tt.url, tt.body); code != tt.want {
			t.Errorf("%s: status %d, want %d", tt.name, code, tt.want)
		}
	}
	if len(sig.sent) != 0 {
		t.Errorf("rejected requests signalled: %+v", sig.sent)
	}

	sig.err = errors.New("temporal down")
	if code := postJSON(t, url(running.ID.String(), "steer"), `{"text":"x"}`); code != http.StatusBadGateway {
		t.Errorf("unreachable workflow: %d", code)
	}
	_, noTemporal, _, _ := testServer(t)
	if code := postJSON(t, noTemporal.URL+"/api/steps/"+running.ID.String()+"/steer", `{"text":"x"}`); code != http.StatusServiceUnavailable {
		t.Errorf("without workflows: %d", code)
	}
}
