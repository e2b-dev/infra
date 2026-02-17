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

func getTeamPrefix(teamID string) string {
	return redis_utils.CreateKey(sandboxKeyPrefix, redis_utils.SameSlot(teamID))
}

func getSandboxKey(teamID, sandboxID string) string {
	return redis_utils.CreateKey(getTeamPrefix(teamID), sandboxesKey, sandboxID)
}

func getTeamIndexKey(teamID string) string {
	return redis_utils.CreateKey(getTeamPrefix(teamID), indexKey)
}

func getTransitionKey(teamID, sandboxID string) string {
	return redis_utils.CreateKey(getTeamPrefix(teamID), transitionKeyPrefix, sandboxID)
}

func getTransitionResultKey(teamID, sandboxID, transitionID string) string {
	return redis_utils.CreateKey(getTransitionKey(teamID, sandboxID), transitionID)
}
