package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// NewQuestion is the input for OpenQuestion: a question event of an agent
// step's session.
type NewQuestion struct {
	StepRunID  uuid.UUID
	QuestionID string // the runner's
	Question   string
}

// PendingQuestion is an unanswered question of a running agent step, with
// what the inbox shows and what the API needs to signal the run's workflow.
type PendingQuestion struct {
	domain.Question
	StepID             string
	IssueKey           string
	IssueTitle         string
	TemporalWorkflowID string
	TemporalRunID      string
}

// OpenQuestion records a question an agent asked. Opening it again (a
// retried Activity that sees the event again) returns the existing row.
func (s *Store) OpenQuestion(ctx context.Context, in NewQuestion) (domain.Question, error) {
	if strings.TrimSpace(in.QuestionID) == "" || strings.TrimSpace(in.Question) == "" {
		return domain.Question{}, errors.New("store: question: question_id and question are required")
	}
	step, err := s.q.GetStepRun(ctx, in.StepRunID)
	if err != nil {
		return domain.Question{}, mapErr("question step run", err)
	}
	row, err := s.q.OpenQuestion(ctx, db.OpenQuestionParams{ID: domain.NewID(), StepRunID: step.ID, RunID: step.RunID,
		QuestionID: in.QuestionID, Question: in.Question})
	if err != nil {
		return domain.Question{}, mapErr("open question", err)
	}
	return questionFromRow(row), nil
}

// GetQuestion returns a question by its ID.
func (s *Store) GetQuestion(ctx context.Context, id uuid.UUID) (domain.Question, error) {
	row, err := s.q.GetQuestion(ctx, id)
	if err != nil {
		return domain.Question{}, mapErr("get question", err)
	}
	return questionFromRow(row), nil
}

// AnswerQuestion records the answer the runner took. Only the first answer
// counts: a different later one returns ErrConflict, and repeating the same
// answer returns it unchanged, so a retried Activity is idempotent.
func (s *Store) AnswerQuestion(ctx context.Context, stepRunID uuid.UUID, questionID, answer, by string) (domain.Question, error) {
	row, err := s.q.AnswerQuestion(ctx, db.AnswerQuestionParams{StepRunID: stepRunID, QuestionID: questionID,
		Answer: &answer, AnsweredBy: nonEmpty(by)})
	if err == nil {
		return questionFromRow(row), nil
	}
	if err = mapErr("answer question", err); !errors.Is(err, ErrNotFound) {
		return domain.Question{}, err
	}
	current, err := s.q.GetQuestionByRunnerID(ctx, db.GetQuestionByRunnerIDParams{StepRunID: stepRunID, QuestionID: questionID})
	if err != nil {
		return domain.Question{}, mapErr("get question", err)
	}
	if q := questionFromRow(current); q.Status == domain.QuestionAnswered && q.Answer == answer {
		return q, nil
	}
	return domain.Question{}, fmt.Errorf("store: question %s of step run %s is already %s: %w", questionID, stepRunID, current.Status, ErrConflict)
}

// ExpireQuestion marks a question nobody can answer any more (its session
// stopped waiting for it). It reports whether the question was pending.
func (s *Store) ExpireQuestion(ctx context.Context, stepRunID uuid.UUID, questionID string) (bool, error) {
	n, err := s.q.ExpireQuestion(ctx, db.ExpireQuestionParams{StepRunID: stepRunID, QuestionID: questionID})
	if err != nil {
		return false, mapErr("expire question", err)
	}
	return n > 0, nil
}

// ListPendingQuestions is the questions inbox, oldest first: unanswered
// questions of agent steps that still run.
func (s *Store) ListPendingQuestions(ctx context.Context) ([]PendingQuestion, error) {
	rows, err := s.q.ListPendingQuestions(ctx)
	if err != nil {
		return nil, mapErr("list pending questions", err)
	}
	out := make([]PendingQuestion, 0, len(rows))
	for _, r := range rows {
		out = append(out, PendingQuestion{Question: questionFromRow(db.Question{ID: r.ID, StepRunID: r.StepRunID, RunID: r.RunID,
			QuestionID: r.QuestionID, Question: r.Question, Status: r.Status, Answer: r.Answer, AnsweredBy: r.AnsweredBy,
			AnsweredAt: r.AnsweredAt, CreatedAt: r.CreatedAt}),
			StepID: r.StepID, IssueKey: r.IssueKey, IssueTitle: r.IssueTitle, TemporalWorkflowID: r.TemporalWorkflowID,
			TemporalRunID: deref(r.TemporalRunID)})
	}
	return out, nil
}

// GetPendingQuestion returns a question that waits for its answer, or
// ErrNotFound when there is none, it was answered, or its step ended.
func (s *Store) GetPendingQuestion(ctx context.Context, id uuid.UUID) (PendingQuestion, error) {
	r, err := s.q.GetPendingQuestion(ctx, id)
	if err != nil {
		return PendingQuestion{}, mapErr("get pending question", err)
	}
	return PendingQuestion{Question: questionFromRow(db.Question{ID: r.ID, StepRunID: r.StepRunID, RunID: r.RunID,
		QuestionID: r.QuestionID, Question: r.Question, Status: r.Status, Answer: r.Answer, AnsweredBy: r.AnsweredBy,
		AnsweredAt: r.AnsweredAt, CreatedAt: r.CreatedAt}),
		StepID: r.StepID, IssueKey: r.IssueKey, IssueTitle: r.IssueTitle, TemporalWorkflowID: r.TemporalWorkflowID,
		TemporalRunID: deref(r.TemporalRunID)}, nil
}

func questionFromRow(r db.Question) domain.Question {
	return domain.Question{ID: r.ID, StepRunID: r.StepRunID, RunID: r.RunID, QuestionID: r.QuestionID, Text: r.Question,
		Status: domain.QuestionStatus(r.Status), Answer: deref(r.Answer), AnsweredBy: deref(r.AnsweredBy),
		AnsweredAt: r.AnsweredAt, CreatedAt: r.CreatedAt}
}

// ListAnsweredQuestions returns the questions people answered for an issue,
// across all its runs, oldest answer first.
func (s *Store) ListAnsweredQuestions(ctx context.Context, issueID uuid.UUID) ([]domain.Question, error) {
	rows, err := s.q.ListAnsweredQuestions(ctx, issueID)
	if err != nil {
		return nil, mapErr("list answered questions", err)
	}
	out := make([]domain.Question, 0, len(rows))
	for _, r := range rows {
		out = append(out, questionFromRow(r))
	}
	return out, nil
}
