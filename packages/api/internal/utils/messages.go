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

func SandboxPausedMsg(sandboxID string) string {
	return fmt.Sprintf("Sandbox %q is paused", sandboxID)
}

func SandboxChangingStateMsg(sandboxID string, state sandbox.State) string {
	switch state {
	case sandbox.StateKilling:
		return fmt.Sprintf("Sandbox %q is being killed", sandboxID)
	case sandbox.StatePausing:
		return fmt.Sprintf("Sandbox %q is being paused", sandboxID)
	case sandbox.StateSnapshotting:
		return fmt.Sprintf("Sandbox %q is being snapshotted", sandboxID)
	default:
		return fmt.Sprintf("Sandbox %q is in %s state", sandboxID, state)
	}
}
