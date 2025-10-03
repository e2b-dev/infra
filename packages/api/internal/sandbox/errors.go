package sandbox

import "fmt"

type AlreadyBeingStartedError struct {
	SandboxID string
}

func (e *AlreadyBeingStartedError) Error() string {
	return fmt.Sprintf("sandbox %s is already being started", e.SandboxID)
}

type LimitExceededError struct {
	TeamID string
}

func (e *LimitExceededError) Error() string {
	return fmt.Sprintf("team %s has exceeded the limit", e.TeamID)
}

type NotFoundError struct {
	SandboxID string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("sandbox %s not found", e.SandboxID)
}
