package runner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

func TestQuestionsAskAndAnswer(t *testing.T) {
	log := NewEventLog(time.Now)
	ended := make(chan struct{})
	q := newQuestions(log, ended, time.Now)

	got := make(chan string, 1)
	go func() {
		a, err := q.ask(t.Context(), "Which port?")
		if err != nil {
			t.Error(err)
		}
		got <- a
	}()
	var id string
	for deadline := time.Now().Add(5 * time.Second); id == ""; {
		if time.Now().After(deadline) {
			t.Fatal("no question event")
		}
		events, _, _ := log.after(0)
		for _, e := range events {
			if e.Type == domain.EventQuestion {
				var p Question
				_ = json.Unmarshal(e.Payload, &p)
				if p.Question != "Which port?" || p.QuestionID == "" {
					t.Fatalf("question payload = %s", e.Payload)
				}
				id = p.QuestionID
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if waiting, _ := q.waiting(); !waiting {
		t.Error("not waiting while the question is open")
	}
	if err := q.answer("nope", "x"); !errors.Is(err, ErrUnknownQuestion) {
		t.Errorf("unknown question: %v", err)
	}
	if err := q.answer(id, "8080"); err != nil {
		t.Fatal(err)
	}
	if a := <-got; a != "8080" {
		t.Errorf("answer = %q", a)
	}
	if err := q.answer(id, "again"); !errors.Is(err, ErrUnknownQuestion) {
		t.Errorf("a second answer: %v", err)
	}
	if waiting, resolved := q.waiting(); waiting || resolved.IsZero() {
		t.Errorf("after the answer: waiting %v, resolved %v", waiting, resolved)
	}
}

func TestQuestionEndsWithTheSession(t *testing.T) {
	ended := make(chan struct{})
	q := newQuestions(NewEventLog(time.Now), ended, time.Now)
	errs := make(chan error, 1)
	go func() {
		_, err := q.ask(context.Background(), "Still there?")
		errs <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(ended)
	if err := <-errs; !errors.Is(err, errSessionEnded) {
		t.Errorf("err = %v", err)
	}
}
