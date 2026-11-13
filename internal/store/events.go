package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/store/db"
)

// NewEvent is the input for AppendEvent. Ts defaults to now; Payload defaults to {}.
type NewEvent struct {
	RunID     *uuid.UUID
	StepRunID *uuid.UUID
	SessionID *uuid.UUID
	Ts        time.Time
	Type      domain.EventType
	Payload   json.RawMessage
}

// AppendEvent adds an event to the append-only stream.
func (s *Store) AppendEvent(ctx context.Context, in NewEvent) (domain.Event, error) {
	if in.Type == "" {
		return domain.Event{}, errors.New("store: append event: empty type")
	}
	if in.Ts.IsZero() {
		in.Ts = time.Now().UTC()
	}
	if len(in.Payload) == 0 {
		in.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(in.Payload) {
		return domain.Event{}, errors.New("store: append event: payload is not valid JSON")
	}
	row, err := s.q.AppendEvent(ctx, db.AppendEventParams{
		ID:        domain.NewID(),
		RunID:     in.RunID,
		StepRunID: in.StepRunID,
		SessionID: in.SessionID,
		Ts:        in.Ts,
		Type:      string(in.Type),
		Payload:   in.Payload,
	})
	if err != nil {
		return domain.Event{}, mapErr("append event", err)
	}
	if err := notifyEvent(ctx, s.q, in.RunID); err != nil {
		return domain.Event{}, err
	}
	return eventFromRow(row), nil
}

// ListRunEvents returns up to limit events of a Run with seq > afterSeq, in seq order.
// Pass afterSeq 0 to replay from the start, or the last seen Seq to resume (FR-OB-2).
func (s *Store) ListRunEvents(ctx context.Context, runID uuid.UUID, afterSeq int64, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("store: list run events: limit %d <= 0", limit)
	}
	rows, err := s.q.ListRunEvents(ctx, db.ListRunEventsParams{
		RunID:     &runID,
		AfterSeq:  afterSeq,
		MaxEvents: int32(min(limit, 10_000)),
	})
	if err != nil {
		return nil, mapErr("list run events", err)
	}
	out := make([]domain.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, eventFromRow(r))
	}
	return out, nil
}

func eventFromRow(r db.Event) domain.Event {
	return domain.Event{
		ID:        r.ID,
		Seq:       r.Seq,
		RunID:     r.RunID,
		StepRunID: r.StepRunID,
		SessionID: r.SessionID,
		Ts:        r.Ts,
		Type:      domain.EventType(r.Type),
		Payload:   r.Payload,
		CreatedAt: r.CreatedAt,
	}
}
