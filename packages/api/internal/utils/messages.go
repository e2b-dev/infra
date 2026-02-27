package utils

import "fmt"

func SandboxNotFoundMsg(sandboxID string) string {
	return fmt.Sprintf("Sandbox %q doesn't exist or you don't have access to it", sandboxID)
}

func SandboxChangingStateMsg(sandboxID string, state string) string {
	return fmt.Sprintf("Sandbox %q is in %s state", sandboxID, state)
}
