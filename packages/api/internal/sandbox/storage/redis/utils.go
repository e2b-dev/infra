package redis

import (
	"fmt"
)

// Helper functions
func getSandboxKey(sandboxID string) string {
	return fmt.Sprintf("%s%s", sandboxKeyPrefix, sandboxID)
}
