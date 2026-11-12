// Package domain holds the platform's entities and their state machines
// (docs/spec/02-domain-model.md). It must stay free of provider types (invariant 7).
package domain

import (
	"errors"

	"github.com/google/uuid"
)

// ErrInvalidTransition reports a status change the state machine does not allow.
var ErrInvalidTransition = errors.New("invalid status transition")

// NewID returns a new time-ordered UUIDv7, the key format for every entity.
func NewID() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}
