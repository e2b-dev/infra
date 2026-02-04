package redis

import (
	"fmt"
	"strings"
)

const (
	separator        = ":"
	sandboxKeyPrefix = "sandbox:storage"
	sandboxesKey     = "sandboxes"
	indexKey         = "index"
)

func createKey(keyParts ...string) string {
	return strings.Join(keyParts, separator)
}

// sameSlot forces Redis to use the same slot for all keys
// this is needed e.g. to use MGet and transactions
func sameSlot(key string) string {
	return fmt.Sprintf("{%s}", key)
}

func getTeamPrefix(teamID string) string {
	return createKey(sandboxKeyPrefix, sameSlot(teamID))
}

func getSandboxKey(teamID, sandboxID string) string {
	return createKey(getTeamPrefix(teamID), sandboxesKey, sandboxID)
}

func getTeamIndexKey(teamID string) string {
	return createKey(getTeamPrefix(teamID), indexKey)
}
