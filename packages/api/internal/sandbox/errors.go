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

var ErrNotFound = errors.New("sandbox not found")

type InvalidStateTransitionError struct {
	CurrentState State
	TargetState  State
}

func (e *InvalidStateTransitionError) Error() string {
	return fmt.Sprintf("invalid state transition from %s to %s", e.CurrentState, e.TargetState)
}

type NotRunningError struct {
	SandboxID string
	State     State
}

func (e *NotRunningError) Error() string {
	return fmt.Sprintf("sandbox %s is not running (state: %s)", e.SandboxID, e.State)
}

var ErrAlreadyExists = errors.New("sandbox already exists")

var ErrEvictionInProgress = errors.New("sandbox eviction already in progress")

var ErrEvictionNotNeeded = errors.New("sandbox eviction not needed")
