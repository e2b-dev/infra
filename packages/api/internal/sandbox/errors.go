package sandbox

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type LimitExceededError struct {
	TeamID uuid.UUID
}

func (e *LimitExceededError) Error() string {
	return fmt.Sprintf("team %s has exceeded the limit", e.TeamID.String())
}

type NotFoundError struct {
	SandboxID string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("sandbox %s not found", e.SandboxID)
}

type InvalidStateTransitionError struct {
	CurrentState State
	TargetState  State
}

func (e *InvalidStateTransitionError) Error() string {
	return fmt.Sprintf("invalid state transition from %s to %s", e.CurrentState, e.TargetState)
}

var (
	ErrAlreadyExists    = errors.New("sandbox already exists")
	ErrCannotShortenTTL = errors.New("cannot shorten ttl")
)
