package sandbox

import (
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// AlreadyBeingStartedError indicates a sandbox is being started or already running.
type AlreadyBeingStartedError struct {
	SandboxID string
	Start     *utils.SetOnce[Sandbox]
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
