package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow"
)

type signal struct {
	workflowID, runID, name string
	arg                     any
}

type fakeSignaler struct {
	sent []signal
	err  error
}

func (f *fakeSignaler) SignalWorkflow(_ context.Context, workflowID, runID, name string, arg any) error {
	f.sent = append(f.sent, signal{workflowID, runID, name, arg})
	return f.err
}

// pendingGate opens a human gate on the test run.
func pendingGate(t *testing.T, st *store.Store, run domain.Run) domain.StepRun {
	t.Helper()
	step, err := st.CreateStepRun(t.Context(), store.NewStepRun{RunID: run.ID, StepID: "approve", Attempt: 1, Kind: domain.StepGate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.OpenApproval(t.Context(), store.NewApproval{StepRunID: step.ID, Reason: "protected paths changed",
		ChangeRequestURL: "https://github.com/o/r/pull/5", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return step
}

func postDecision(t *testing.T, url, contentType, body string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestApprovalsInbox(t *testing.T) {
	st, srv, run, _ := testServer(t)
	step := pendingGate(t, st, run)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/api/approvals", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got []approvalJSON
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].StepRunID != step.ID || got[0].IssueKey != "gc-1" || got[0].Step != "approve" ||
		got[0].ChangeRequestURL != "https://github.com/o/r/pull/5" || got[0].RunID != run.ID {
		t.Errorf("inbox = %+v", got)
	}
}

// FR-WF-3: a decision reaches the run's workflow as a signal.
func TestDecideGateSignalsTheWorkflow(t *testing.T) {
	sig := &fakeSignaler{}
	st, srv, run, _ := testServerWith(t, sig)
	step := pendingGate(t, st, run)
	url := srv.URL + "/api/gates/" + step.ID.String() + "/decision"

	if code := postDecision(t, url, "application/json", `{"decision":"approve","comment":" ok ","by":"alice"}`); code != http.StatusAccepted {
		t.Fatalf("status = %d", code)
	}
	if len(sig.sent) != 1 {
		t.Fatalf("signals = %+v", sig.sent)
	}
	got := sig.sent[0]
	want := workflow.HumanDecision{StepRunID: step.ID.String(), Approved: true, By: "alice", Comment: "ok"}
	if got.workflowID != run.TemporalWorkflowID || got.name != workflow.HumanDecisionSignal || got.arg != want {
		t.Errorf("signal = %+v", got)
	}
}

func TestDecideGateRejects(t *testing.T) {
	sig := &fakeSignaler{}
	st, srv, run, _ := testServerWith(t, sig)
	step := pendingGate(t, st, run)
	url := srv.URL + "/api/gates/" + step.ID.String() + "/decision"
	tests := []struct {
		name, url, contentType, body string
		want                         int
	}{
		{"form post from another site", url, "application/x-www-form-urlencoded", "decision=approve", http.StatusUnsupportedMediaType},
		{"unknown decision", url, "application/json", `{"decision":"maybe"}`, http.StatusBadRequest},
		{"unknown field", url, "application/json", `{"decision":"approve","force":true}`, http.StatusBadRequest},
		{"bad id", srv.URL + "/api/gates/nope/decision", "application/json", `{"decision":"approve"}`, http.StatusBadRequest},
		{"no such gate", srv.URL + "/api/gates/" + domain.NewID().String() + "/decision", "application/json", `{"decision":"approve"}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code := postDecision(t, tt.url, tt.contentType, tt.body); code != tt.want {
				t.Errorf("status = %d, want %d", code, tt.want)
			}
		})
	}
	if len(sig.sent) != 0 {
		t.Errorf("signals sent for invalid requests: %+v", sig.sent)
	}

	t.Run("already decided", func(t *testing.T) {
		if _, err := st.DecideApproval(t.Context(), step.ID, domain.ApprovalRejected, "bob", ""); err != nil {
			t.Fatal(err)
		}
		if code := postDecision(t, url, "application/json", `{"decision":"approve"}`); code != http.StatusConflict {
			t.Errorf("status = %d, want 409", code)
		}
	})
}

func TestDecideGateWithoutWorkflows(t *testing.T) {
	t.Run("no signaler", func(t *testing.T) {
		st, srv, run, _ := testServer(t)
		step := pendingGate(t, st, run)
		if code := postDecision(t, srv.URL+"/api/gates/"+step.ID.String()+"/decision", "application/json", `{"decision":"reject"}`); code != http.StatusServiceUnavailable {
			t.Errorf("status = %d", code)
		}
	})
	t.Run("signal fails", func(t *testing.T) {
		st, srv, run, _ := testServerWith(t, &fakeSignaler{err: errors.New("workflow not found")})
		step := pendingGate(t, st, run)
		if code := postDecision(t, srv.URL+"/api/gates/"+step.ID.String()+"/decision", "application/json", `{"decision":"reject"}`); code != http.StatusBadGateway {
			t.Errorf("status = %d", code)
		}
	})
}
