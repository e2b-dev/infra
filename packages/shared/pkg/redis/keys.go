package redis_utils

import (
	"fmt"
	"strings"
)

const separator = ":"

// CreateKey joins key parts with the standard Redis key separator.
func CreateKey(keyParts ...string) string {
	return strings.Join(keyParts, separator)
}

// SameSlot wraps a key in curly braces to force Redis Cluster hash slot co-location.
// This is needed to use Lua scripts, MGET, and transactions across multiple keys.
func SameSlot(key string) string {
	return fmt.Sprintf("{%s}", key)
}
