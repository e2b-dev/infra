package redis

import (
	"fmt"
)

// Helper functions
func getSandboxKey(sandboxID string) string {
	return fmt.Sprintf("%s%s", sandboxKeyPrefix, sandboxID)
}

// Helper functions
func getSandboxLockKey(sandboxID string) string {
	return fmt.Sprintf("%s%s", sandboxLockKeyPrefix, sandboxID)
}
