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

var ErrSandboxKilled = errors.New("sandbox was killed")

// KillReason represents why a sandbox was terminated.
type KillReason string

const (
	// KillReasonAPI indicates the sandbox was terminated via API request.
	KillReasonAPI KillReason = "api"
	// KillReasonEvicted indicates the sandbox was evicted due to timeout expiration.
	KillReasonEvicted KillReason = "timeout"
)

// KillInfo contains information about when and why a sandbox was killed.
type KillInfo struct {
	Reason   KillReason
	KilledAt int64 // Unix timestamp in milliseconds
}

// TransitionReason represents why a sandbox transitioned to a new state.
type TransitionReason string

const (
	// TransitionReasonAPI indicates the transition was triggered via API request.
	TransitionReasonAPI TransitionReason = "api"
	// TransitionReasonTimeout indicates the transition was triggered due to timeout expiration.
	TransitionReasonTimeout TransitionReason = "timeout"
)

// TransitionInfo contains information about an ongoing state transition.
type TransitionInfo struct {
	ToState   State            `json:"toState"`
	Reason    TransitionReason `json:"reason"`
	StartedAt int64            `json:"startedAt"` // Unix timestamp in milliseconds
}

type InvalidStateTransitionError struct {
	CurrentState State
	TargetState  State
	Transition   *TransitionInfo
}

func (e *InvalidStateTransitionError) Error() string {
	return fmt.Sprintf("invalid state transition from %s to %s", e.CurrentState, e.TargetState)
}

type NotRunningError struct {
	SandboxID  string
	State      State
	Transition *TransitionInfo
}

func (e *NotRunningError) Error() string {
	return fmt.Sprintf("sandbox %s is not running (state: %s)", e.SandboxID, e.State)
}

var ErrAlreadyExists = errors.New("sandbox already exists")

var ErrEvictionInProgress = errors.New("sandbox eviction already in progress")

var ErrEvictionNotNeeded = errors.New("sandbox eviction not needed")
