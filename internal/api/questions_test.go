package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store"
	"github.com/misarescu/frankie/internal/workflow"
)

// askedQuestion records a question the agent of a step asked.
func askedQuestion(t *testing.T, st *store.Store, step domain.StepRun, questionID string) domain.Question {
	t.Helper()
	q, err := st.OpenQuestion(t.Context(), store.NewQuestion{StepRunID: step.ID, QuestionID: questionID, Question: "Which port should the server use?"})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

// D55: the inbox lists the questions of running steps, and an answer becomes
// a signal to the run's workflow naming the step and the runner's question.
func TestQuestionsInboxAndAnswer(t *testing.T) {
	sig := &fakeSignaler{}
	st, srv, run, _ := testServerWith(t, sig)
	step := stepIn(t, st, run, "implement", domain.StepAgent, domain.ExecRunning)
	q := askedQuestion(t, st, step, "q1")
	ended := stepIn(t, st, run, "review", domain.StepAgent, domain.ExecRunning)
	askedQuestion(t, st, ended, "q2")
	if _, err := st.TransitionStepRun(t.Context(), ended.ID, domain.ExecRunning, domain.ExecSucceeded); err != nil {
		t.Fatal(err)
	}

	var got []questionJSON
	if code := getJSON(t, srv.URL+"/api/questions", &got); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	if len(got) != 1 || got[0].ID != q.ID || got[0].Step != "implement" || got[0].RunID != run.ID ||
		got[0].Question != "Which port should the server use?" || got[0].IssueKey == "" {
		t.Fatalf("inbox = %+v", got)
	}

	if code := postJSON(t, srv.URL+"/api/questions/"+q.ID.String()+"/answer", `{"text":" 8080 ","by":"ana"}`); code != http.StatusAccepted {
		t.Fatalf("answer: %d", code)
	}
	want := signal{run.TemporalWorkflowID, run.TemporalRunID, workflow.AnswerSignal,
		workflow.StepIntervention{StepRunID: step.ID.String(), QuestionID: "q1", By: "ana", Text: "8080"}}
	if len(sig.sent) != 1 || sig.sent[0] != want {
		t.Errorf("signals = %+v, want %+v", sig.sent, want)
	}
}

func TestAnswerRejects(t *testing.T) {
	sig := &fakeSignaler{}
	st, srv, run, _ := testServerWith(t, sig)
	step := stepIn(t, st, run, "implement", domain.StepAgent, domain.ExecRunning)
	pending := askedQuestion(t, st, step, "q1")
	answered := askedQuestion(t, st, step, "q2")
	if _, err := st.AnswerQuestion(t.Context(), step.ID, "q2", "yes", "bo"); err != nil {
		t.Fatal(err)
	}
	expired := askedQuestion(t, st, step, "q3")
	if _, err := st.ExpireQuestion(t.Context(), step.ID, "q3"); err != nil {
		t.Fatal(err)
	}
	url := func(id string) string { return srv.URL + "/api/questions/" + id + "/answer" }
	tests := []struct {
		name, url, body string
		want            int
	}{
		{"no text", url(pending.ID.String()), `{"text":"  "}`, http.StatusBadRequest},
		{"unknown field", url(pending.ID.String()), `{"text":"x","force":true}`, http.StatusBadRequest},
		{"too long", url(pending.ID.String()), `{"text":"` + strings.Repeat("x", maxInterventionText+1) + `"}`, http.StatusBadRequest},
		{"bad id", url("nope"), `{"text":"x"}`, http.StatusBadRequest},
		{"unknown question", url("0198f2a4-0000-7000-8000-000000000000"), `{"text":"x"}`, http.StatusNotFound},
		{"answered", url(answered.ID.String()), `{"text":"x"}`, http.StatusConflict},
		{"expired", url(expired.ID.String()), `{"text":"x"}`, http.StatusConflict},
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
	if code := postJSON(t, url(pending.ID.String()), `{"text":"x"}`); code != http.StatusBadGateway {
		t.Errorf("unreachable workflow: %d", code)
	}
	_, noTemporal, _, _ := testServer(t)
	if code := postJSON(t, noTemporal.URL+"/api/questions/"+pending.ID.String()+"/answer", `{"text":"x"}`); code != http.StatusServiceUnavailable {
		t.Errorf("without workflows: %d", code)
	}
}
