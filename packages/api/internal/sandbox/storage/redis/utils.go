package redis

import (
	"strings"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/sandboxtypes"
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

const (
	sandboxKeyPrefix    = "sandbox:storage"
	transitionKeyPrefix = "transition"
	lockKeyPrefix       = "lock"
	notifySuffix        = "notify"
	sandboxesKey        = "sandboxes"
	indexKey            = "index"
)

var (
	// globalStorageNotifyChannel is the single Redis PubSub channel used by
	// storage notifications. The per-event routing key is embedded in the message
	// payload so one connection per API pod is sufficient.
	globalStorageNotifyChannel = redis_utils.CreateKey(sandboxKeyPrefix, notifySuffix)

	globalTeamsSet      = redis_utils.CreateKey(sandboxKeyPrefix, "global:teams")
	globalExpirationSet = redis_utils.CreateKey(sandboxKeyPrefix, "global:expiration")
)

// expirationMember identifies one *execution* (incarnation) of a sandbox in
// the global expiration ZSET. Scoping the member to the execution makes every
// ZREM structurally safe: removing a dead execution's member can never
// unindex a live one, even when a lockless Add for the same sandbox ID races
// a Remove or the evictor's stale sweep.
func expirationMember(teamID, sandboxID, executionID string) string {
	return redis_utils.CreateKey(teamID, sandboxID, executionID)
}

// legacyExpirationMember is the pre-executionID member format still present
// in live data (and written by old pods during a rolling deploy). It drains
// via Remove's dual ZREM and the lazy upgrade in ExpiredItems.
//
// TODO [EN-1602]: remove the legacy member format once the migration is complete
// Then delete:
//   - legacyExpirationMember and the ExecutionID == "" fallback in
//     sandboxExpirationMember (utils.go)
//   - the executionID == "" legacy branch in parseExpirationMember (utils.go)
//   - the legacy member in Remove's dual ZREM (operations.go)
//   - the migrate-on-write ZRem in Update (operations.go)
//   - the legacy upgrade path in ExpiredItems: upgrades/upgradedLegacy and
//     the sweptLegacyUpgraded metric attribute (items.go, main.go)
//   - the legacyMember half of the ZMSCORE existence check in
//     healTeamExpirationIndex (heal.go)
//   - the legacy cases in expiration_index_test.go
func legacyExpirationMember(teamID, sandboxID string) string {
	return redis_utils.CreateKey(teamID, sandboxID)
}

// sandboxExpirationMember returns the expiration index member for a stored
// sandbox, falling back to the legacy format when ExecutionID is absent.
func sandboxExpirationMember(sbx sandboxtypes.Sandbox) string {
	if sbx.ExecutionID == "" {
		return legacyExpirationMember(sbx.TeamID.String(), sbx.SandboxID)
	}

	return expirationMember(sbx.TeamID.String(), sbx.SandboxID, sbx.ExecutionID)
}

// parseExpirationMember parses both member formats:
// "teamID:sandboxID:executionID" (current) and "teamID:sandboxID" (legacy).
// executionID == "" marks a legacy member. Sandbox IDs never contain ':'
// and execution IDs are always UUIDs.
func parseExpirationMember(member string) (teamID, sandboxID, executionID string, ok bool) {
	parts := strings.Split(member, ":")
	switch len(parts) {
	case 2: // legacy
		if parts[1] == "" {
			return "", "", "", false
		}

		return parts[0], parts[1], "", true
	case 3:
		if _, err := uuid.Parse(parts[2]); err != nil {
			return "", "", "", false
		}

		return parts[0], parts[1], parts[2], true
	default:
		return "", "", "", false
	}
}

// GetTeamPrefix returns the storage team prefix for external packages (e.g. reservations).
func GetTeamPrefix(teamID string) string {
	return redis_utils.CreateKey(sandboxKeyPrefix, redis_utils.SameSlot(teamID))
}

func getSandboxKey(teamID, sandboxID string) string {
	return redis_utils.CreateKey(GetTeamPrefix(teamID), sandboxesKey, sandboxID)
}

// GetSandboxStorageTeamIndexKey returns the storage team index key for external packages (e.g. reservations).
func GetSandboxStorageTeamIndexKey(teamID string) string {
	return redis_utils.CreateKey(GetTeamPrefix(teamID), indexKey)
}

func getTransitionKey(teamID, sandboxID string) string {
	return redis_utils.CreateKey(GetTeamPrefix(teamID), transitionKeyPrefix, sandboxID)
}

func getTransitionResultKey(teamID, sandboxID, transitionID string) string {
	return redis_utils.CreateKey(getTransitionKey(teamID, sandboxID), transitionID)
}

// getTransitionRoutingKey returns the per-transition routing key embedded in the
// payload of messages published to globalStorageNotifyChannel. Including
// transitionID ensures that notifications for one transition can never wake
// waiters subscribed to a different transition for the same sandbox.
func getTransitionRoutingKey(teamID, sandboxID, transitionID string) string {
	return redis_utils.CreateKey(GetTeamPrefix(teamID), transitionKeyPrefix, sandboxID, transitionID, notifySuffix)
}

func getLockRoutingKey(lockKey string) string {
	return redis_utils.CreateKey(lockKeyPrefix, lockKey, notifySuffix)
}
