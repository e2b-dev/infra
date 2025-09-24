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
	return fmt.Sprintf("sandbox %s has exceeded the limit", e.TeamID)
}
