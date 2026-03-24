package redis

import (
	"strings"

	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

const (
	sandboxKeyPrefix    = "sandbox:storage"
	transitionKeyPrefix = "transition"
	notifySuffix        = "notify"
	sandboxesKey        = "sandboxes"
	indexKey            = "index"
)

var (
	// globalTransitionNotifyChannel is the single Redis PubSub channel used by
	// all transitions. The per-transition routing key is embedded in the message
	// payload so one connection per API pod is sufficient.
	globalTransitionNotifyChannel = redis_utils.CreateKey(sandboxKeyPrefix, transitionKeyPrefix, notifySuffix)

	globalTeamsSet      = redis_utils.CreateKey(sandboxKeyPrefix, "global:teams")
	globalExpirationSet = redis_utils.CreateKey(sandboxKeyPrefix, "global:expiration")
)

func expirationMember(teamID, sandboxID string) string {
	return redis_utils.CreateKey(teamID, sandboxID)
}

func parseExpirationMember(member string) (teamID, sandboxID string, ok bool) {
	teamID, sandboxID, ok = strings.Cut(member, ":")

	return
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
// payload of messages published to globalTransitionNotifyChannel. Including
// transitionID ensures that notifications for one transition can never wake
// waiters subscribed to a different transition for the same sandbox.
func getTransitionRoutingKey(teamID, sandboxID, transitionID string) string {
	return redis_utils.CreateKey(GetTeamPrefix(teamID), transitionKeyPrefix, sandboxID, transitionID, notifySuffix)
}
