package utils

import (
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

func SandboxNotFoundMsg(sandboxID string) string {
	return fmt.Sprintf("Sandbox %q doesn't exist or you don't have access to it", sandboxID)
}

func SandboxKilledMsg(sandboxID string, info *sandbox.KillInfo) string {
	if info == nil {
		return fmt.Sprintf("Sandbox %q was killed and is no longer available", sandboxID)
	}

	killedAt := time.UnixMilli(info.KilledAt).UTC().Format(time.RFC3339)

	switch info.Reason {
	case sandbox.KillReasonEvicted:
		return fmt.Sprintf("Sandbox %q was killed at %s due to timeout expiration", sandboxID, killedAt)
	case sandbox.KillReasonAPI:
		return fmt.Sprintf("Sandbox %q was killed at %s via API request", sandboxID, killedAt)
	default:
		return fmt.Sprintf("Sandbox %q was killed at %s and is no longer available", sandboxID, killedAt)
	}
}

func SandboxChangingStateMsg(sandboxID string, transition *sandbox.TransitionInfo) string {
	if transition == nil {
		return fmt.Sprintf("Sandbox %q is in a transitional state", sandboxID)
	}

	startedAt := time.UnixMilli(transition.StartedAt).UTC().Format(time.RFC3339)
	reasonStr := transitionReasonString(transition.Reason)

	switch transition.ToState {
	case sandbox.StateKilling:
		return fmt.Sprintf("Sandbox %q is being killed %s (started at %s)", sandboxID, reasonStr, startedAt)
	case sandbox.StatePausing:
		return fmt.Sprintf("Sandbox %q is being paused %s (started at %s)", sandboxID, reasonStr, startedAt)
	case sandbox.StateSnapshotting:
		return fmt.Sprintf("Sandbox %q is being snapshotted %s (started at %s)", sandboxID, reasonStr, startedAt)
	default:
		return fmt.Sprintf("Sandbox %q is transitioning to %s state %s (started at %s)", sandboxID, transition.ToState, reasonStr, startedAt)
	}
}

func transitionReasonString(reason sandbox.TransitionReason) string {
	switch reason {
	case sandbox.TransitionReasonAPI:
		return "via API request"
	case sandbox.TransitionReasonTimeout:
		return "due to timeout expiration"
	default:
		return ""
	}
}
