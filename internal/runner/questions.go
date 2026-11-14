package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/misarescu/frankie/internal/domain"
)

// ErrUnknownQuestion is returned for an answer to no waiting question.
var ErrUnknownQuestion = errors.New("runner: no such question is waiting")

// errSessionEnded is how a waiting question ends when its session does.
var errSessionEnded = errors.New("the session ended before anyone answered")

// questions are a session's waiting ask-human questions: each is announced
// as a question event and waits for its answer, or for the session to end.
type questions struct {
	log   *EventLog
	ended <-chan struct{}
	now   func() time.Time

	mu       sync.Mutex
	pending  map[string]chan string
	resolved time.Time // when the last question stopped waiting
}

func newQuestions(log *EventLog, ended <-chan struct{}, now func() time.Time) *questions {
	return &questions{log: log, ended: ended, now: now, pending: map[string]chan string{}}
}

// ask announces a question and waits for its answer.
func (q *questions) ask(ctx context.Context, question string) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	answer := make(chan string, 1)
	q.mu.Lock()
	q.pending[id] = answer
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		delete(q.pending, id)
		q.resolved = q.now()
		q.mu.Unlock()
	}()
	q.log.Append(domain.EventQuestion, Question{QuestionID: id, Question: question})
	select {
	case a := <-answer:
		return a, nil
	case <-q.ended:
		return "", errSessionEnded
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// answer delivers the answer to a waiting question.
func (q *questions) answer(id, text string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	ch, ok := q.pending[id]
	if !ok {
		return ErrUnknownQuestion
	}
	delete(q.pending, id)
	ch <- text
	return nil
}

// waiting reports whether a question waits for its answer, and when the
// last one stopped waiting: idle time does not count while a person thinks.
func (q *questions) waiting() (bool, time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending) > 0, q.resolved
}
