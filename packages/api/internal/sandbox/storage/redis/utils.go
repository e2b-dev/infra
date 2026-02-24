package redis

import (
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

const (
	sandboxKeyPrefix    = "sandbox:storage"
	transitionKeyPrefix = "transition:"
	sandboxesKey        = "sandboxes"
	indexKey            = "index"
)

var globalTeamsSet = redis_utils.CreateKey(sandboxKeyPrefix, "global:teams")

// GetTeamPrefix returns the storage team prefix for external packages (e.g. reservations).
func GetTeamPrefix(teamID string) string {
	return redis_utils.CreateKey(sandboxKeyPrefix, redis_utils.SameSlot(teamID))
}

func getSandboxKey(teamID, sandboxID string) string {
	return redis_utils.CreateKey(GetTeamPrefix(teamID), sandboxesKey, sandboxID)
}

func getTeamIndexKey(teamID string) string {
	return GetSandboxStorageTeamIndexKey(teamID)
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
