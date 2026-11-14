package store

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
)

// runningAgentStep creates a running agent StepRun of a new run.
func runningAgentStep(t *testing.T, s *Store) (domain.Run, domain.Issue, domain.StepRun) {
	t.Helper()
	ctx := t.Context()
	is := mustIssue(t, s, mustProject(t, s).ID, domain.IssueRunning)
	run := mustRun(t, s, is.ID)
	step, err := s.CreateStepRun(ctx, NewStepRun{RunID: run.ID, StepID: "implement", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatal(err)
	}
	if step, err = s.TransitionStepRun(ctx, step.ID, domain.ExecPending, domain.ExecRunning); err != nil {
		t.Fatal(err)
	}
	return run, is, step
}

// D55: a question is opened once per runner question, waits in the inbox
// while its step runs, and takes the first answer only.
func TestQuestionLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	run, is, step := runningAgentStep(t, s)
	in := NewQuestion{StepRunID: step.ID, QuestionID: "q1", Question: "Which port should the server use?"}

	q, err := s.OpenQuestion(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.OpenQuestion(ctx, in)
	if err != nil || again.ID != q.ID || q.Status != domain.QuestionPending || q.RunID != run.ID {
		t.Fatalf("open = %+v, reopen = %+v, %v", q, again, err)
	}

	pending, err := s.ListPendingQuestions(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	p := pending[0]
	if p.StepID != "implement" || p.IssueKey != is.Key || p.TemporalWorkflowID != run.TemporalWorkflowID || p.Text != in.Question {
		t.Errorf("pending question = %+v", p)
	}
	if got, err := s.GetPendingQuestion(ctx, q.ID); err != nil || got.QuestionID != "q1" {
		t.Errorf("GetPendingQuestion = %+v, %v", got, err)
	}

	a, err := s.AnswerQuestion(ctx, step.ID, "q1", "8080", "ana")
	if err != nil || a.Status != domain.QuestionAnswered || a.Answer != "8080" || a.AnsweredBy != "ana" || a.AnsweredAt == nil {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	// The same answer again (a retried Activity) is fine; another one is not.
	if _, err := s.AnswerQuestion(ctx, step.ID, "q1", "8080", "ana"); err != nil {
		t.Errorf("repeated answer: %v", err)
	}
	if _, err := s.AnswerQuestion(ctx, step.ID, "q1", "9090", "bo"); !errors.Is(err, ErrConflict) {
		t.Errorf("second answer: err = %v, want ErrConflict", err)
	}
	if expired, err := s.ExpireQuestion(ctx, step.ID, "q1"); err != nil || expired {
		t.Errorf("expiring an answered question = %v, %v", expired, err)
	}
	if _, err := s.GetPendingQuestion(ctx, q.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("answered question still pending: %v", err)
	}
	if left, _ := s.ListPendingQuestions(ctx); len(left) != 0 {
		t.Errorf("inbox after the answer = %+v", left)
	}
	if _, err := s.AnswerQuestion(ctx, step.ID, "nope", "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("answer to an unknown question: err = %v, want ErrNotFound", err)
	}
}

// D55: a question nobody answered expires, explicitly or when its step run
// finishes, and leaves the inbox.
func TestQuestionsExpire(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	_, _, step := runningAgentStep(t, s)
	for _, id := range []string{"q1", "q2"} {
		if _, err := s.OpenQuestion(ctx, NewQuestion{StepRunID: step.ID, QuestionID: id, Question: "Why?"}); err != nil {
			t.Fatal(err)
		}
	}

	if expired, err := s.ExpireQuestion(ctx, step.ID, "q1"); err != nil || !expired {
		t.Fatalf("expire q1 = %v, %v", expired, err)
	}
	if left, _ := s.ListPendingQuestions(ctx); len(left) != 1 || left[0].QuestionID != "q2" {
		t.Fatalf("inbox = %+v", left)
	}

	if _, err := s.TransitionStepRun(ctx, step.ID, domain.ExecRunning, domain.ExecFailed); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.ListPendingQuestions(ctx); len(left) != 0 {
		t.Errorf("inbox after the step ended = %+v", left)
	}
	q2, err := s.OpenQuestion(ctx, NewQuestion{StepRunID: step.ID, QuestionID: "q2", Question: "Why?"})
	if err != nil || q2.Status != domain.QuestionExpired {
		t.Errorf("q2 after the step ended = %+v, %v", q2, err)
	}
	if got, err := s.GetQuestion(ctx, q2.ID); err != nil || got.Status != domain.QuestionExpired {
		t.Errorf("GetQuestion = %+v, %v", got, err)
	}
}

func TestOpenQuestionValidates(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	_, _, step := runningAgentStep(t, s)
	if _, err := s.OpenQuestion(ctx, NewQuestion{StepRunID: step.ID, QuestionID: "q1"}); err == nil {
		t.Error("a question without text was accepted")
	}
	if _, err := s.OpenQuestion(ctx, NewQuestion{StepRunID: uuid.New(), QuestionID: "q1", Question: "Why?"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown step run: err = %v, want ErrNotFound", err)
	}
}

// D56: an issue's answered questions, across its runs and oldest answer
// first, are what later steps get; pending and expired questions, and other
// issues' answers, are not.
func TestListAnsweredQuestions(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	run, is, first := runningAgentStep(t, s)
	// The issue runs again once its first run failed.
	if _, err := s.TransitionRun(ctx, run.ID, run.Status, domain.ExecFailed); err != nil {
		t.Fatal(err)
	}
	again := mustRun(t, s, is.ID)
	second, err := s.CreateStepRun(ctx, NewStepRun{RunID: again.ID, StepID: "review", Attempt: 1, Kind: domain.StepAgent})
	if err != nil {
		t.Fatal(err)
	}
	_, _, other := runningAgentStep(t, s)
	ask := func(step domain.StepRun, id, text string) {
		t.Helper()
		if _, err := s.OpenQuestion(ctx, NewQuestion{StepRunID: step.ID, QuestionID: id, Question: text}); err != nil {
			t.Fatal(err)
		}
	}
	ask(first, "q1", "Which field name?")
	ask(first, "q2", "Never answered?")
	ask(first, "q3", "Expired?")
	ask(second, "q4", "Which status code?")
	ask(other, "q5", "Another issue?")
	for _, a := range []struct {
		step   domain.StepRun
		id, to string
	}{{second, "q4", "200"}, {first, "q1", "book_count"}, {other, "q5", "x"}} {
		if _, err := s.AnswerQuestion(ctx, a.step.ID, a.id, a.to, "ana"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ExpireQuestion(ctx, first.ID, "q3"); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListAnsweredQuestions(ctx, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].QuestionID != "q4" || got[0].Answer != "200" || got[1].QuestionID != "q1" || got[1].Answer != "book_count" {
		t.Errorf("answered = %+v", got)
	}
}
